package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/gorm"

	"iptv-tool-v2/internal/iptv"
	"iptv-tool-v2/internal/model"
	epgpkg "iptv-tool-v2/pkg/epg"
)

// EPGSourceService handles fetching and updating EPG sources
type EPGSourceService struct{}

func NewEPGSourceService() *EPGSourceService {
	return &EPGSourceService{}
}

// FetchAndUpdate fetches the EPG source data and updates the database
func (s *EPGSourceService) FetchAndUpdate(sourceID uint) error {
	run, err := s.PrepareFetchAndUpdate(sourceID)
	if err != nil {
		return err
	}
	return run()
}

// PrepareFetchAndUpdate claims the source for syncing and returns the work to run.
// This lets manual API triggers report claim failures before starting a goroutine.
func (s *EPGSourceService) PrepareFetchAndUpdate(sourceID uint) (func() error, error) {
	var source model.EPGSource
	if err := model.DB.First(&source, sourceID).Error; err != nil {
		return nil, fmt.Errorf("epg source %d not found: %w", sourceID, err)
	}

	if !source.Status {
		return func() error { return nil }, nil // Source is disabled, skip
	}

	releaseSyncing, err := claimEPGSourceSyncing(sourceID)
	if err != nil {
		return nil, err
	}

	return func() error {
		return s.fetchAndUpdateClaimed(source, releaseSyncing)
	}, nil
}

func claimEPGSourceSyncing(sourceID uint) (func(), error) {
	claim := model.DB.Model(&model.EPGSource{}).
		Where("id = ? AND is_syncing = ?", sourceID, false).
		Update("is_syncing", true)
	if claim.Error != nil {
		return nil, fmt.Errorf("failed to mark EPG source syncing: %w", claim.Error)
	}
	if claim.RowsAffected == 0 {
		return nil, fmt.Errorf("error.source_syncing")
	}
	return func() {
		model.DB.Model(&model.EPGSource{}).Where("id = ?", sourceID).Update("is_syncing", false)
	}, nil
}

func (s *EPGSourceService) fetchAndUpdateClaimed(source model.EPGSource, releaseSyncing func()) error {
	defer func() {
		// Defensive cleanup
		releaseSyncing()
	}()

	var programs []epgpkg.Program
	var fetchErr error

	switch source.Type {
	case model.EPGSourceTypeNetworkXMLTV:
		programs, fetchErr = s.fetchNetworkXMLTV(source.URL, source.Headers)
	case model.EPGSourceTypeIPTV:
		// Acquire per-LiveSourceID mutex to ensure mutual exclusion with the
		// associated IPTV live source (IPTV servers reject concurrent auth)
		if source.LiveSourceID != nil {
			slog.Info("Acquiring IPTV lock for EPG source", "id", source.ID, "live_source_id", *source.LiveSourceID)
			unlock := AcquireIPTVLock(*source.LiveSourceID)
			defer unlock()
			slog.Info("Acquired IPTV lock for EPG source", "id", source.ID, "live_source_id", *source.LiveSourceID)
		}
		programs, fetchErr = s.fetchIPTVEPG(source)
	default:
		fetchErr = fmt.Errorf("unsupported EPG source type: %s", source.Type)
	}

	now := time.Now()
	if fetchErr != nil {
		model.DB.Model(&source).Updates(map[string]interface{}{
			"last_error":      fetchErr.Error(),
			"last_fetched_at": &now,
		})
		return fetchErr
	}

	// Save parsed EPG to database
	if err := s.saveParsedEPG(source.ID, programs); err != nil {
		return err
	}

	// Update last fetch time and clear error
	model.DB.Model(&source).Updates(map[string]interface{}{
		"last_fetched_at": &now,
		"last_error":      "",
	})

	slog.Info("EPG source fetched successfully", "name", source.Name, "id", source.ID, "programs", len(programs))
	return nil
}

func (s *EPGSourceService) fetchNetworkXMLTV(url string, headersJSON string) ([]epgpkg.Program, error) {
	// FetchAndParseXMLTV automatically handles gzip detection (via HTTP Header and magic number)
	programs, err := epgpkg.FetchAndParseXMLTV(url, headersJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch/parse XMLTV from %s: %w", url, err)
	}
	return programs, nil
}

func (s *EPGSourceService) fetchIPTVEPG(source model.EPGSource) ([]epgpkg.Program, error) {
	var config iptv.Config
	if err := json.Unmarshal([]byte(source.IPTVConfig), &config); err != nil {
		return nil, fmt.Errorf("failed to parse IPTV EPG config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Create IPTV client using the factory (pass pointer so strategy auto-detect can write back)
	client, err := createIPTVClient(&config)
	if err != nil {
		return nil, err
	}

	// Authenticate first
	if err := client.Authenticate(ctx); err != nil {
		return nil, fmt.Errorf("IPTV authentication failed: %w", err)
	}

	// Get channel list (needed to know which channels to fetch EPG for)
	iptvChannels, err := client.GetAllChannelList(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channel list for EPG: %w", err)
	}

	// Fetch EPG using the configured strategy (with rate limiting and retry built in)
	progLists, err := client.GetAllChannelProgramList(ctx, iptvChannels)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch IPTV EPG: %w", err)
	}

	// Convert iptv.ChannelProgramList to epgpkg.Program for unified storage
	var programs []epgpkg.Program
	for _, pl := range progLists {
		for _, prog := range pl.Programs {
			programs = append(programs, epgpkg.Program{
				Channel:     pl.Channel.ID,
				ChannelName: pl.Channel.Name,
				Title:       prog.Title,
				Desc:        prog.Desc,
				StartTime:   prog.StartTime,
				EndTime:     prog.EndTime,
			})
		}
	}

	// If auto-detect found a working strategy, persist it back so next run skips detection.
	// Use surgical map update to preserve all existing config fields (e.g. authParams, headers).
	if config.EPGStrategy != "" && config.EPGStrategy != "auto" {
		var configMap map[string]interface{}
		if err := json.Unmarshal([]byte(source.IPTVConfig), &configMap); err == nil {
			configMap["epgStrategy"] = config.EPGStrategy
			if merged, err := json.Marshal(configMap); err == nil {
				model.DB.Model(&source).Update("iptv_config", string(merged))
				slog.Info("EPG auto-detect: persisted working strategy", "strategy", config.EPGStrategy, "epg_source_id", source.ID)
			}
		}
	}

	return programs, nil
}

func (s *EPGSourceService) saveParsedEPG(sourceID uint, programs []epgpkg.Program) error {
	// Batch insert new EPG records
	var records []model.ParsedEPG
	for _, prog := range programs {
		records = append(records, model.ParsedEPG{
			SourceID:    sourceID,
			Channel:     strings.TrimSpace(prog.Channel),
			ChannelName: strings.TrimSpace(prog.ChannelName),
			Title:       prog.Title,
			Desc:        prog.Desc,
			StartTime:   prog.StartTime,
			EndTime:     prog.EndTime,
		})
	}

	// Wrap delete + insert in a single transaction for atomicity and reduced fsync overhead.
	// Without this, a crash between delete and insert would leave the source with no EPG data.
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("source_id = ?", sourceID).Delete(&model.ParsedEPG{}).Error; err != nil {
			return fmt.Errorf("failed to clear old EPG data: %w", err)
		}
		if len(records) > 0 {
			if err := tx.CreateInBatches(records, 200).Error; err != nil {
				return fmt.Errorf("failed to save parsed EPG: %w", err)
			}
		}
		return nil
	})
}
