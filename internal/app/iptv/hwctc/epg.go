package hwctc

import (
	"context"
	"errors"
	"iptv/internal/app/iptv"

	"go.uber.org/zap"
)

var (
	ErrParseChProgList   = errors.New("failed to parse channel program list")
	ErrChProgListIsEmpty = errors.New("the list of programs is empty")
	ErrEPGApiNotFound    = errors.New("epg api not found")
)

const (
	maxBackDay = 8

	chProgAPILiveplay   = "liveplay_30"
	chProgAPIGdhdpublic = "gdhdpublic"
	chProgAPIVsp        = "vsp"
	chProgAPIShandong   = "shandong"
)

// GetAllChannelProgramList 获取所有频道的节目单列表
func (c *Client) GetAllChannelProgramList(ctx context.Context, channels []iptv.Channel) ([]iptv.ChannelProgramList, error) {
	// 请求认证的Token
	token, err := c.requestToken(ctx)
	if err != nil {
		return nil, err
	}

	epg := make([]iptv.ChannelProgramList, 0, len(channels))
	for _, channel := range channels {
		// 跳过不支持回看的频道
		if channel.TimeShift != "1" || channel.TimeShiftLength <= 0 {
			continue
		}

		var progList *iptv.ChannelProgramList
		switch c.config.ChannelProgramAPI {
		case chProgAPILiveplay:
			progList, err = c.getLiveplayChannelProgramList(ctx, token, &channel)
		case chProgAPIGdhdpublic:
			progList, err = c.getGdhdpublicChannelProgramList(ctx, token, &channel)
		case chProgAPIVsp:
			progList, err = c.getVspChannelProgramList(ctx, token, &channel)
		case chProgAPIShandong:
			progList, err = c.getSdIptvChannelProgramList(ctx, token, &channel)
		default:
			// 自动选择调用EPG的API接口
			progList, err = c.getChannelProgramListByAuto(ctx, token, &channel)
		}

		if err != nil {
			if errors.Is(err, ErrEPGApiNotFound) {
				c.logger.Error("Failed to get channel program list.", zap.Error(err))
				break
			}
			c.logger.Sugar().Warnf("Failed to get the program list for channel %s. Error: %v", channel.ChannelName, err)
			continue
		}

		if progList != nil && len(progList.DateProgramList) > 0 {
			epg = append(epg, *progList)
		}
	}

	return epg, nil
}

// getChannelProgramListByAuto 自动选择调用EPG的API接口
func (c *Client) getChannelProgramListByAuto(ctx context.Context, token *Token, channel *iptv.Channel) (*iptv.ChannelProgramList, error) {
	progList, err := c.getLiveplayChannelProgramList(ctx, token, channel)
	if !errors.Is(err, ErrEPGApiNotFound) {
		c.logger.Info("An available EPG API was found.", zap.String("channelProgramAPI", chProgAPILiveplay))
		c.config.ChannelProgramAPI = chProgAPILiveplay
		return progList, err
	}

	progList, err = c.getGdhdpublicChannelProgramList(ctx, token, channel)
	if !errors.Is(err, ErrEPGApiNotFound) {
		c.logger.Info("An available EPG API was found.", zap.String("channelProgramAPI", chProgAPIGdhdpublic))
		c.config.ChannelProgramAPI = chProgAPIGdhdpublic
		return progList, err
	}

	progList, err = c.getVspChannelProgramList(ctx, token, channel)
	if !errors.Is(err, ErrEPGApiNotFound) {
		c.logger.Info("An available EPG API was found.", zap.String("channelProgramAPI", chProgAPIVsp))
		c.config.ChannelProgramAPI = chProgAPIVsp
		return progList, err
	}

	return nil, err
}
