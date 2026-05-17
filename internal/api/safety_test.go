package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iptv-tool-v2/internal/model"
	"iptv-tool-v2/internal/service"
)

func setupAPIDB(t *testing.T, models ...interface{}) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api-test.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap test db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	if len(models) == 0 {
		models = []interface{}{
			&model.LiveSource{},
			&model.EPGSource{},
			&model.ChannelLogo{},
			&model.PublishInterface{},
			&model.AggregationRule{},
			&model.ParsedChannel{},
			&model.ParsedEPG{},
			&model.SystemSetting{},
			&model.AccessControlEntry{},
		}
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	model.DB = db
	globalACLCache.invalidate()
}

func TestParseLogoUploadFilenameRejectsUnsafeNames(t *testing.T) {
	bad := []string{"../evil.png", `..\evil.png`, "", "safe..bad.png", ".png"}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			if got, _, err := parseLogoUploadFilename(name); err == nil {
				t.Fatalf("expected error for %q, got %q", name, got)
			}
		})
	}

	name, ext, err := parseLogoUploadFilename("CCTV.png")
	if err != nil {
		t.Fatalf("valid filename rejected: %v", err)
	}
	if name != "CCTV" || ext != ".png" {
		t.Fatalf("got name=%q ext=%q", name, ext)
	}
}

func TestPublishPathValidation(t *testing.T) {
	setupAPIDB(t, &model.PublishInterface{})
	gin.SetMode(gin.TestMode)

	router := gin.New()
	ctrl := NewPublishController(nil)
	router.POST("/publish", ctrl.CreateInterface)

	body := `{"name":"bad","path":"../bad","type":"live","format":"m3u"}`
	req := httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unsafe path status = %d, body=%s", w.Code, w.Body.String())
	}

	body = `{"name":"good","path":"my-list.1","type":"live","format":"m3u"}`
	req = httptest.NewRequest(http.MethodPost, "/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("valid path status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestImportAccessControlInvalidatesCacheAndRejectsSelfLockout(t *testing.T) {
	setupAPIDB(t, &model.SystemSetting{}, &model.AccessControlEntry{})
	gin.SetMode(gin.TestMode)

	if err := model.DB.Create(&model.SystemSetting{Key: "access_control_mode", Value: "disabled"}).Error; err != nil {
		t.Fatalf("seed ACL mode: %v", err)
	}
	mode, _ := globalACLCache.get()
	if mode != "disabled" {
		t.Fatalf("initial cached mode = %q", mode)
	}

	router := gin.New()
	ctrl := NewConfigTransferController(nil, t.TempDir())
	router.POST("/config/import/execute", ctrl.ImportExecute)

	zipData := makeACLImportZip(t, "blacklist", []model.AccessControlEntry{{
		ListType:  "blacklist",
		EntryType: "single",
		Value:     "203.0.113.10",
	}})
	w := postImportZip(t, router, zipData, "198.51.100.2:1234")
	if w.Code != http.StatusOK {
		t.Fatalf("valid ACL import status = %d, body=%s", w.Code, w.Body.String())
	}

	mode, entries := globalACLCache.get()
	if mode != "blacklist" || len(entries) != 1 || entries[0].Value != "203.0.113.10" {
		t.Fatalf("ACL cache was not refreshed, mode=%q entries=%+v", mode, entries)
	}

	zipData = makeACLImportZip(t, "whitelist", []model.AccessControlEntry{{
		ListType:  "whitelist",
		EntryType: "single",
		Value:     "203.0.113.10",
	}})
	w = postImportZip(t, router, zipData, "198.51.100.2:1234")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("self-locking ACL import status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestUploadInvalidFFprobePreservesExistingFile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	detectDir := filepath.Join(dataDir, "detect")
	if err := os.MkdirAll(detectDir, 0755); err != nil {
		t.Fatalf("mkdir detect dir: %v", err)
	}
	targetName := "ffprobe"
	if runtime.GOOS == "windows" {
		targetName = "ffprobe.exe"
	}
	targetPath := filepath.Join(detectDir, targetName)
	if err := os.WriteFile(targetPath, []byte("old-ffprobe"), 0644); err != nil {
		t.Fatalf("write old ffprobe: %v", err)
	}

	router := gin.New()
	ctrl := NewSettingsController(dataDir)
	router.POST("/settings/detect/ffprobe", ctrl.UploadFFprobe)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", targetName)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write([]byte("not an executable")); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/detect/ffprobe", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid ffprobe status = %d, body=%s", w.Code, w.Body.String())
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read old ffprobe: %v", err)
	}
	if string(content) != "old-ffprobe" {
		t.Fatalf("old ffprobe was overwritten: %q", content)
	}
}

func makeACLImportZip(t *testing.T, mode string, entries []model.AccessControlEntry) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeZipJSONForTest(t, zw, "iptv-config/manifest.json", service.ExportManifest{
		Version: "test",
		Modules: []string{service.ModuleAccessControl},
	})
	writeZipJSONForTest(t, zw, "iptv-config/access_control.json", map[string]interface{}{
		"mode":    mode,
		"entries": entries,
	})
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func writeZipJSONForTest(t *testing.T, zw *zip.Writer, name string, v interface{}) {
	t.Helper()

	fw, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create zip entry %s: %v", name, err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal zip entry %s: %v", name, err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("write zip entry %s: %v", name, err)
	}
}

func postImportZip(t *testing.T, router *gin.Engine, zipData []byte, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "config.zip")
	if err != nil {
		t.Fatalf("create multipart zip: %v", err)
	}
	if _, err := part.Write(zipData); err != nil {
		t.Fatalf("write multipart zip: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart zip writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/config/import/execute", &body)
	req.RemoteAddr = remoteAddr
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
