package api

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"iptv-tool-v2/internal/model"
	"iptv-tool-v2/internal/service"
	"iptv-tool-v2/pkg/i18n"
)

// SettingsController handles system settings for detection configuration
type SettingsController struct {
	dataDir       string
	detectService *service.DetectService
}

func NewSettingsController(dataDir string) *SettingsController {
	return &SettingsController{
		dataDir:       dataDir,
		detectService: service.NewDetectService(dataDir),
	}
}

// GetDetectSettings returns the current detection configuration
// GET /api/settings/detect
func (sc *SettingsController) GetDetectSettings(c *gin.Context) {
	concurrency := strconv.Itoa(service.DefaultDetectConcurrency)
	timeout := strconv.Itoa(service.DefaultDetectTimeout)

	var settings []model.SystemSetting
	model.DB.Where("key IN ?", []string{"detect_concurrency", "detect_timeout"}).Find(&settings)

	for _, s := range settings {
		switch s.Key {
		case "detect_concurrency":
			concurrency = s.Value
		case "detect_timeout":
			timeout = s.Value
		}
	}

	// Get ffprobe version if available
	ffprobeVersion := ""
	ffprobeSource := ""
	if ver, source, err := sc.detectService.GetFFprobeVersion(); err == nil {
		ffprobeVersion = ver
		ffprobeSource = source
	}

	concurrencyInt, _ := strconv.Atoi(concurrency)
	timeoutInt, _ := strconv.Atoi(timeout)

	c.JSON(http.StatusOK, gin.H{
		"concurrency":     concurrencyInt,
		"timeout":         timeoutInt,
		"ffprobe_version": ffprobeVersion,
		"ffprobe_source":  ffprobeSource,
	})
}

// UpdateDetectSettings updates the detection configuration
// PUT /api/settings/detect
func (sc *SettingsController) UpdateDetectSettings(c *gin.Context) {
	var req struct {
		Concurrency *int `json:"concurrency"`
		Timeout     *int `json:"timeout"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Concurrency != nil {
		if *req.Concurrency < 1 || *req.Concurrency > 30 {
			c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.concurrency_range")})
			return
		}
		sc.upsertSetting("detect_concurrency", strconv.Itoa(*req.Concurrency))
	}

	if req.Timeout != nil {
		if *req.Timeout < 1 || *req.Timeout > 30 {
			c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.timeout_range")})
			return
		}
		sc.upsertSetting("detect_timeout", strconv.Itoa(*req.Timeout))
	}

	c.JSON(http.StatusOK, gin.H{"message": i18n.T(i18n.Lang(c), "message.detect_config_updated")})
}

// UploadFFprobe handles ffprobe executable file upload
// POST /api/settings/detect/ffprobe
func (sc *SettingsController) UploadFFprobe(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.select_upload_file")})
		return
	}

	// Determine target filename
	targetName := "ffprobe"
	if runtime.GOOS == "windows" {
		targetName = "ffprobe.exe"
	}

	// Ensure detect directory exists
	detectDir := filepath.Join(sc.dataDir, "detect")
	if err := os.MkdirAll(detectDir, 0755); err != nil {
		slog.Error("Failed to create detect directory", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": i18n.T(i18n.Lang(c), "error.mkdir_failed")})
		return
	}

	targetPath := filepath.Join(detectDir, targetName)
	targetExt := filepath.Ext(targetName)
	targetBase := strings.TrimSuffix(targetName, targetExt)
	tmpPath := filepath.Join(detectDir, "."+targetBase+".upload-"+strconv.FormatInt(time.Now().UnixNano(), 10)+targetExt)

	// Save to a temporary path first. The existing ffprobe is replaced only
	// after the uploaded file has been verified successfully.
	if err := c.SaveUploadedFile(file, tmpPath); err != nil {
		slog.Error("Failed to save ffprobe file", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": i18n.T(i18n.Lang(c), "error.save_file_failed")})
		return
	}
	defer os.Remove(tmpPath)

	// Set executable permission (Unix-like systems)
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0755); err != nil {
			slog.Warn("Failed to set executable permission", "error", err)
		}
	}

	// Verify the uploaded file is actually ffprobe
	version, err := sc.detectService.ValidateFFprobePath(tmpPath)
	if err != nil {
		errMsg := "unrecognized file"
		if err != nil {
			errMsg = err.Error()
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": i18n.T(i18n.Lang(c), "error.invalid_ffprobe", errMsg)})
		return
	}

	if err := replaceFilePreservingExisting(tmpPath, targetPath); err != nil {
		slog.Error("Failed to replace ffprobe file", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": i18n.T(i18n.Lang(c), "error.save_file_failed")})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":         i18n.T(i18n.Lang(c), "message.ffprobe_uploaded"),
		"ffprobe_version": version,
		"ffprobe_source":  "uploaded",
	})
}

func replaceFilePreservingExisting(tmpPath, targetPath string) error {
	backupPath := targetPath + ".bak-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	hadExisting := false

	if _, err := os.Stat(targetPath); err == nil {
		hadExisting = true
		if err := os.Rename(targetPath, backupPath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		if hadExisting {
			_ = os.Rename(backupPath, targetPath)
		}
		return err
	}

	if hadExisting {
		_ = os.Remove(backupPath)
	}
	return nil
}

// upsertSetting creates or updates a system setting
func (sc *SettingsController) upsertSetting(key, value string) {
	var setting model.SystemSetting
	result := model.DB.Where("key = ?", key).First(&setting)
	if result.Error != nil {
		// Create new
		model.DB.Create(&model.SystemSetting{Key: key, Value: value})
	} else {
		// Update existing
		model.DB.Model(&setting).Update("value", value)
	}
}
