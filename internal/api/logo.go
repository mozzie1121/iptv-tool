package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"iptv-tool-v2/internal/model"
	"iptv-tool-v2/internal/publish"
	"iptv-tool-v2/pkg/i18n"
	"iptv-tool-v2/pkg/utils"
)

var (
	errInvalidLogoFilename = errors.New("invalid logo filename")
	errUnsupportedLogoExt  = errors.New("unsupported logo file extension")
)

var allowedLogoExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".webp": true, ".ico": true,
}

func parseLogoUploadFilename(filename string) (name string, ext string, err error) {
	safeName, err := utils.SafeBaseFilename(filename)
	if err != nil {
		return "", "", errInvalidLogoFilename
	}

	originalExt := filepath.Ext(safeName)
	ext = strings.ToLower(originalExt)
	if !allowedLogoExts[ext] {
		return "", "", errUnsupportedLogoExt
	}

	name = strings.TrimSpace(strings.TrimSuffix(safeName, originalExt))
	if name == "" {
		return "", "", errInvalidLogoFilename
	}
	return name, ext, nil
}

// LogoController handles channel logo CRUD and upload
type LogoController struct {
	logoDir string // Directory where logos are stored (e.g., "logos/")
}

func NewLogoController(logoDir string) *LogoController {
	// Ensure directory exists
	os.MkdirAll(logoDir, 0755)
	return &LogoController{logoDir: logoDir}
}

// List returns all channel logos
// GET /api/logos
func (lc *LogoController) List(c *gin.Context) {
	var logos []model.ChannelLogo
	if err := model.DB.Order("id desc").Find(&logos).Error; err != nil {
		slog.Error("Internal server error", "error", err, "path", c.Request.URL.Path)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, logos)
}

// Upload handles logo file upload
// POST /api/logos/upload
func (lc *LogoController) Upload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.select_file")})
		return
	}

	name, ext, err := parseLogoUploadFilename(file.Filename)
	if errors.Is(err, errInvalidLogoFilename) {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.invalid_logo_filename")})
		return
	}
	if errors.Is(err, errUnsupportedLogoExt) {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.unsupported_file_type")})
		return
	}

	// Check for logo name uniqueness
	var existing int64
	model.DB.Model(&model.ChannelLogo{}).Where("name = ?", name).Count(&existing)
	if existing > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": i18n.T(i18n.Lang(c), "error.logo_name_exists_upload", name)})
		return
	}

	fileName := fmt.Sprintf("%s_%d%s", name, model.DB.NowFunc().UnixMilli(), ext)
	filePath, err := utils.SafeJoinWithinDir(lc.logoDir, fileName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.invalid_logo_filename")})
		return
	}
	urlPath := "/logo/" + fileName

	if err := c.SaveUploadedFile(file, filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": i18n.T(i18n.Lang(c), "error.file_save_failed") + ": " + err.Error()})
		return
	}

	logo := model.ChannelLogo{
		Name:     name,
		FilePath: filePath,
		URLPath:  urlPath,
	}

	if err := model.DB.Create(&logo).Error; err != nil {
		os.Remove(filePath) // Clean up file on DB error
		slog.Error("Internal server error", "error", err, "path", c.Request.URL.Path)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	publish.InvalidateAll()
	c.JSON(http.StatusCreated, logo)
}

// BatchUpload handles multiple logo file uploads
// POST /api/logos/batch-upload
func (lc *LogoController) BatchUpload(c *gin.Context) {
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.form_parse_failed")})
		return
	}

	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.no_files_provided")})
		return
	}

	var uploaded []model.ChannelLogo
	var uploadErrors []string

	for _, file := range files {
		name, ext, err := parseLogoUploadFilename(file.Filename)
		if errors.Is(err, errInvalidLogoFilename) {
			uploadErrors = append(uploadErrors, i18n.T(i18n.Lang(c), "error.invalid_logo_filename")+": "+file.Filename)
			continue
		}
		if errors.Is(err, errUnsupportedLogoExt) {
			uploadErrors = append(uploadErrors, i18n.T(i18n.Lang(c), "error.batch_unsupported_type", file.Filename))
			continue
		}

		// Check for logo name uniqueness
		var existing int64
		model.DB.Model(&model.ChannelLogo{}).Where("name = ?", name).Count(&existing)
		if existing > 0 {
			uploadErrors = append(uploadErrors, i18n.T(i18n.Lang(c), "error.batch_name_exists", file.Filename))
			continue
		}

		fileName := fmt.Sprintf("%s_%d%s", name, model.DB.NowFunc().UnixMilli(), ext)
		filePath, err := utils.SafeJoinWithinDir(lc.logoDir, fileName)
		if err != nil {
			uploadErrors = append(uploadErrors, i18n.T(i18n.Lang(c), "error.invalid_logo_filename")+": "+file.Filename)
			continue
		}
		urlPath := "/logo/" + fileName

		if err := c.SaveUploadedFile(file, filePath); err != nil {
			uploadErrors = append(uploadErrors, i18n.T(i18n.Lang(c), "error.batch_save_failed", file.Filename))
			continue
		}

		logo := model.ChannelLogo{
			Name:     name,
			FilePath: filePath,
			URLPath:  urlPath,
		}
		if err := model.DB.Create(&logo).Error; err != nil {
			os.Remove(filePath)
			uploadErrors = append(uploadErrors, i18n.T(i18n.Lang(c), "error.batch_db_error", file.Filename))
			continue
		}

		uploaded = append(uploaded, logo)
	}

	if len(uploaded) > 0 {
		publish.InvalidateAll()
	}
	c.JSON(http.StatusOK, gin.H{
		"uploaded": uploaded,
		"errors":   uploadErrors,
	})
}

// UpdateLogoRequest is the request body for updating a logo
type UpdateLogoRequest struct {
	Name string `json:"name" binding:"required"`
}

// Update modifies a channel logo's name
// PUT /api/logos/:id
func (lc *LogoController) Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.invalid_id")})
		return
	}

	var req UpdateLogoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.invalid_request_params")})
		return
	}

	// Trim whitespace from string inputs
	req.Name = strings.TrimSpace(req.Name)

	// Check name uniqueness (excluding self)
	var existing int64
	model.DB.Model(&model.ChannelLogo{}).Where("name = ? AND id != ?", req.Name, id).Count(&existing)
	if existing > 0 {
		c.JSON(http.StatusConflict, gin.H{"error": i18n.T(i18n.Lang(c), "error.logo_name_exists")})
		return
	}

	var logo model.ChannelLogo
	if err := model.DB.First(&logo, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": i18n.T(i18n.Lang(c), "error.logo_not_found")})
		return
	}

	logo.Name = req.Name
	if err := model.DB.Save(&logo).Error; err != nil {
		slog.Error("Internal server error", "error", err, "path", c.Request.URL.Path)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	publish.InvalidateAll()
	c.JSON(http.StatusOK, logo)
}

// Delete removes a channel logo
// DELETE /api/logos/:id
func (lc *LogoController) Delete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.invalid_id")})
		return
	}

	var logo model.ChannelLogo
	if err := model.DB.First(&logo, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": i18n.T(i18n.Lang(c), "error.logo_not_found")})
		return
	}

	// Delete file from disk
	os.Remove(logo.FilePath)

	if err := model.DB.Delete(&logo).Error; err != nil {
		slog.Error("Internal server error", "error", err, "path", c.Request.URL.Path)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	publish.InvalidateAll()
	c.JSON(http.StatusOK, gin.H{"message": i18n.T(i18n.Lang(c), "message.logo_deleted")})
}

// LogoBatchDeleteRequest is the request body for batch deleting logos
type LogoBatchDeleteRequest struct {
	IDs []uint `json:"ids" binding:"required,min=1"`
}

// BatchDelete removes multiple channel logos at once
// POST /api/logos/batch-delete
func (lc *LogoController) BatchDelete(c *gin.Context) {
	var req LogoBatchDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.invalid_request_params")})
		return
	}

	// Fetch all logos to delete
	var logos []model.ChannelLogo
	if err := model.DB.Where("id IN ?", req.IDs).Find(&logos).Error; err != nil {
		slog.Error("Internal server error", "error", err, "path", c.Request.URL.Path)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(logos) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": i18n.T(i18n.Lang(c), "error.logo_not_found")})
		return
	}

	// Delete files from disk
	for _, logo := range logos {
		os.Remove(logo.FilePath)
	}

	// Delete records from DB
	if err := model.DB.Where("id IN ?", req.IDs).Delete(&model.ChannelLogo{}).Error; err != nil {
		slog.Error("Internal server error", "error", err, "path", c.Request.URL.Path)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	publish.InvalidateAll()
	c.JSON(http.StatusOK, gin.H{
		"message": i18n.T(i18n.Lang(c), "message.logos_batch_deleted", len(logos)),
		"count":   len(logos),
	})
}
