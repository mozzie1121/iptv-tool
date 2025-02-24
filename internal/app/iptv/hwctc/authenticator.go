package hwctc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iptv/internal/app/iptv"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Token struct {
	UserToken  string `json:"userToken"`
	TempKey    string `json:"tempKey"`
	Stbid      string `json:"stbid"`
	JSESSIONID string `json:"jsessionid"`
}

// requestToken 请求认证的Token
func (c *Client) requestToken(ctx context.Context) (*Token, error) {
	// 访问登录页面
	referer, err := c.authenticationURL(ctx, true)
	if err != nil {
		return nil, err
	}

	// 获取EncryptToken
	encryptToken, err := c.authLoginHWCTC(ctx, referer)
	if err != nil {
		return nil, err
	}

	// 认证并获取Token和JSESSIONID
	return c.validAuthenticationHWCTC(ctx, encryptToken)
}

// authenticationURL 认证第一步
func (c *Client) authenticationURL(ctx context.Context, FCCSupport bool) (string, error) {
	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://%s/EDS/jsp/AuthenticationURL", c.originHost), nil)
	if err != nil {
		return "", err
	}

	// 增加请求参数
	params := req.URL.Query()
	params.Add("UserID", c.config.UserID)
	params.Add("Action", "Login")
	if FCCSupport {
		params.Add("FCCSupport", "1")
	}
	req.URL.RawQuery = params.Encode()

	// 设置请求头
	c.setCommonHeaders(req)

	// 执行请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status code: %d", resp.StatusCode)
	}

	// 服务器会302重定向，这里缓存最新的服务器地址和端口
	c.host = resp.Request.URL.Host

	return resp.Request.URL.String(), nil
}

// authLoginHWCTC 认证第二步
func (c *Client) authLoginHWCTC(ctx context.Context, referer string) (string, error) {
	// 组装请求数据
	data := map[string]string{
		"UserID": c.config.UserID,
		"VIP":    c.config.Vip,
	}
	body := url.Values{}
	for k, v := range data {
		body.Add(k, v)
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s/EPG/jsp/authLoginHWCU.jsp", c.host), strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}

	// 设置请求头
	c.setCommonHeaders(req)
	req.Header.Set("Referer", referer)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// 执行请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http status code: %d", resp.StatusCode)
	}

	// 解析响应内容
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	regex := regexp.MustCompile("EncryptToken = \"(.+?)\";")
	matches := regex.FindSubmatch(result)
	if len(matches) != 2 {
		return "", errors.New("failed to parse EncryptToken")
	}
	return string(matches[1]), nil
}

// validAuthenticationHWCTC 认证第三步，获取UserToken和cookie中的JSESSIONID
func (c *Client) validAuthenticationHWCTC(ctx context.Context, encryptToken string) (*Token, error) {
	// 生成随机的8位数字
	random := c.generate8DigitNumber()

	var err error
	// 获取IPv4地址
	var ipv4Addr string
	if c.config.InterfaceName != "" {
		ipv4Addr, err = c.getInterfaceIPv4Addr(c.config.InterfaceName)
		if err != nil {
			return nil, err
		}
	}
	if ipv4Addr == "" {
		ipv4Addr = c.config.IP
	}

	// 输入的格式：random + "$" + EncryptToken + "$" + UserID + "$" + STBID + "$" + IP + "$" + MAC + "$" + Reserved + "$" + CTC
	input := fmt.Sprintf("%d$%s$%s$%s$%s$%s$$CTC",
		random, encryptToken, c.config.UserID, c.config.STBID, ipv4Addr, c.config.MAC)
	// 使用3DES加密生成Authenticator
	crypto := iptv.NewTripleDESCrypto(c.key)
	authenticator, err := crypto.ECBEncrypt(input)
	if err != nil {
		return nil, err
	}

	// 组装请求数据
	data := map[string]string{
		"UserID":           c.config.UserID,
		"Lang":             c.config.Lang,
		"SupportHD":        "1",
		"NetUserID":        c.config.NetUserID,
		"Authenticator":    strings.ToUpper(authenticator),
		"STBType":          c.config.STBType,
		"STBVersion":       c.config.STBVersion,
		"conntype":         c.config.Conntype,
		"STBID":            c.config.STBID,
		"templateName":     c.config.TemplateName,
		"areaId":           c.config.AreaId,
		"userToken":        encryptToken,
		"userGroupId":      c.config.UserGroupId,
		"productPackageId": c.config.ProductPackageId,
		"mac":              c.config.MAC,
		"UserField":        c.config.UserField,
		"SoftwareVersion":  c.config.SoftwareVersion,
		"IsSmartStb":       c.config.IsSmartStb,
		"desktopId":        "",
		"stbmaker":         "",
		"XMPPCapability":   "",
		"ChipID":           "",
		"VIP":              c.config.Vip,
	}
	body := url.Values{}
	for k, v := range data {
		body.Add(k, v)
	}

	// 创建请求
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://%s/EPG/jsp/ValidAuthenticationHWCU.jsp", c.host), strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}

	// 设置请求头
	c.setCommonHeaders(req)
	referer := fmt.Sprintf("http://%s/EPG/jsp/authLoginHWCU.jsp", c.host)
	req.Header.Set("Referer", referer)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// 执行请求
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status code: %d", resp.StatusCode)
	}

	// 从Cookie中获取JSESSIONID
	var jsessionID string
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "JSESSIONID" {
			jsessionID = cookie.Value
			break
		}
	}
	if jsessionID == "" {
		return nil, errors.New("failed to find JSESSIONID in response")
	}

	// 解析响应内容
	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	regex := regexp.MustCompile("(?s)\"UserToken\" value=\"(.+?)\".+?\"tempKey\" value=\"(.*?)\".+?\"stbid\" value=\"(.*?)\"")
	matches := regex.FindSubmatch(result)
	if len(matches) != 4 {
		return nil, errors.New("failed to parse userToken")
	}
	return &Token{
		UserToken:  string(matches[1]),
		TempKey:    string(matches[2]),
		Stbid:      string(matches[3]),
		JSESSIONID: jsessionID,
	}, nil
}

// generate8DigitNumber 生成随机8位数字
func (c *Client) generate8DigitNumber() int {
	// 设置随机数种子
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// 生成一个8位数字 (范围：10000000 - 99999999)
	number := r.Intn(90000000) + 10000000

	return number
}

// getInterfaceIPv4Addr 获取指定网络接口的IPv4地址
func (c *Client) getInterfaceIPv4Addr(interfaceName string) (string, error) {
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return "", err
	}

	// 获取网络接口的所有地址
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}

	var ipv4Addr string
	// 遍历所有地址
	for _, addr := range addrs {
		// 检查地址类型是否是IPv4
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			ipv4Addr = ipnet.IP.String()
			// 输出IPv4地址
			c.logger.Sugar().Infof("Use network interface %s, IPv4 address: %s", iface.Name, ipv4Addr)
			break
		}
	}

	if ipv4Addr == "" {
		return "", errors.New("address of the specified interface could not found")
	}
	return ipv4Addr, nil
}
