package service

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"iptv-tool-v2/internal/model"
)

func setupServiceDB(t *testing.T, models ...interface{}) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/service-test.db"), &gorm.Config{})
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
			&model.ParsedChannel{},
			&model.ParsedEPG{},
			&model.PublishInterface{},
			&model.SystemSetting{},
			&model.AccessControlEntry{},
			&model.ChannelLogo{},
			&model.AggregationRule{},
		}
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	model.DB = db
}

func TestFetchDisabledSourcesDoNotSetSyncing(t *testing.T) {
	setupServiceDB(t)

	live := model.LiveSource{Name: "disabled-live", Type: model.LiveSourceTypeNetworkManual, Status: false}
	if err := model.DB.Create(&live).Error; err != nil {
		t.Fatalf("create live source: %v", err)
	}
	model.DB.Model(&live).Update("status", false)
	if err := NewLiveSourceService().FetchAndUpdate(live.ID); err != nil {
		t.Fatalf("fetch disabled live source: %v", err)
	}
	var gotLive model.LiveSource
	model.DB.First(&gotLive, live.ID)
	if gotLive.IsSyncing {
		t.Fatal("disabled live source should not be left syncing")
	}

	epg := model.EPGSource{Name: "disabled-epg", Type: model.EPGSourceTypeNetworkXMLTV, Status: false}
	if err := model.DB.Create(&epg).Error; err != nil {
		t.Fatalf("create epg source: %v", err)
	}
	model.DB.Model(&epg).Update("status", false)
	if err := NewEPGSourceService().FetchAndUpdate(epg.ID); err != nil {
		t.Fatalf("fetch disabled epg source: %v", err)
	}
	var gotEPG model.EPGSource
	model.DB.First(&gotEPG, epg.ID)
	if gotEPG.IsSyncing {
		t.Fatal("disabled epg source should not be left syncing")
	}
}

func TestClaimSourceDetectingAllowsOnlyOneConcurrentOwner(t *testing.T) {
	setupServiceDB(t)

	source := model.LiveSource{Name: "detect", Type: model.LiveSourceTypeNetworkManual, Status: true}
	if err := model.DB.Create(&source).Error; err != nil {
		t.Fatalf("create source: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var successes atomic.Int32
	var releasesMu sync.Mutex
	var releases []func()

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			release, err := claimSourceDetecting(source.ID)
			if err != nil {
				if err.Error() != "error.source_detecting" {
					t.Errorf("unexpected claim error: %v", err)
				}
				return
			}
			successes.Add(1)
			releasesMu.Lock()
			releases = append(releases, release)
			releasesMu.Unlock()
		}()
	}

	close(start)
	wg.Wait()

	if successes.Load() != 1 {
		t.Fatalf("expected exactly one detecting owner, got %d", successes.Load())
	}
	for _, release := range releases {
		release()
	}

	var reloaded model.LiveSource
	model.DB.First(&reloaded, source.ID)
	if reloaded.IsDetecting {
		t.Fatal("detecting flag should be cleared after release")
	}
}

func TestImportPublishOverwriteRestoresTokenAndUnicastFields(t *testing.T) {
	setupServiceDB(t, &model.PublishInterface{})

	existing := model.PublishInterface{
		Name:      "main",
		Path:      "old",
		Type:      "live",
		Format:    model.PublishFormatM3U,
		Status:    true,
		SourceIDs: "1",
	}
	if err := model.DB.Create(&existing).Error; err != nil {
		t.Fatalf("create existing publish interface: %v", err)
	}

	data := &ImportParsedData{
		PublishIfaces: []ImportPublishInterface{{
			PublishInterface: model.PublishInterface{
				Name:              "main",
				Path:              "new.slug",
				Type:              "live",
				Format:            model.PublishFormatM3U,
				Status:            true,
				UnicastType:       "proxy",
				UnicastProxyRules: `[{"pattern":"^rtsp://(.+)$","replacement":"http://proxy/${1}"}]`,
				TokenCheckEnabled: true,
				TokenValue:        "secret-token",
			},
		}},
	}

	svc := NewConfigTransferService(t.TempDir(), nil)
	result := svc.importPublish(data, nil, nil, nil)
	if result.Success != 1 || result.Failed != 0 {
		t.Fatalf("unexpected import result: %+v", result)
	}

	var got model.PublishInterface
	model.DB.First(&got, existing.ID)
	if got.UnicastType != "proxy" {
		t.Fatalf("unicast_type = %q", got.UnicastType)
	}
	if !strings.Contains(got.UnicastProxyRules, "proxy") {
		t.Fatalf("unicast_proxy_rules not restored: %q", got.UnicastProxyRules)
	}
	if !got.TokenCheckEnabled || got.TokenValue != "secret-token" {
		t.Fatalf("token fields not restored: enabled=%v value=%q", got.TokenCheckEnabled, got.TokenValue)
	}
}

func TestSanitizeImportLogoFilenameRejectsTraversal(t *testing.T) {
	bad := []string{"../evil.png", `..\evil.png`, "", "safe..bad.png", "logo.txt"}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			if got, err := sanitizeImportLogoFilename(name); err == nil {
				t.Fatalf("expected error for %q, got %q", name, got)
			}
		})
	}

	got, err := sanitizeImportLogoFilename("safe.png")
	if err != nil {
		t.Fatalf("valid logo filename rejected: %v", err)
	}
	if got != "safe.png" {
		t.Fatalf("got %q", got)
	}
}
