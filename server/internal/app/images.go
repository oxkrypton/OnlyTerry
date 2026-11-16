package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	_ "golang.org/x/image/webp"
)

type ImageStore struct {
	directory    string
	maxBytes     int64
	maxPixels    int
	allowPrivate bool
	client       *http.Client
	sem          chan struct{}
}

func NewImageStore(config Config) *ImageStore {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	return &ImageStore{directory: config.ImageDir, maxBytes: config.MaxImageBytes, maxPixels: config.MaxImagePixels, allowPrivate: config.AllowPrivateImageURLs, client: client, sem: make(chan struct{}, 1)}
}

func privateIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 0 || (v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127) || v4[0] >= 224
	}
	return false
}
func (s *ImageStore) checkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return nil, errors.New("图片 URL 协议不受支持")
	}
	if s.allowPrivate {
		return u, nil
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return nil, errors.New("图片 URL 指向本机")
	}
	if ip := net.ParseIP(host); ip != nil {
		if privateIP(ip) {
			return nil, errors.New("图片 URL 指向内网")
		}
		return u, nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("无法解析图片域名")
	}
	for _, address := range addresses {
		if privateIP(address.IP) {
			return nil, errors.New("图片 URL 指向内网")
		}
	}
	return u, nil
}
func (s *ImageStore) download(raw string) ([]byte, string, error) {
	u, err := s.checkURL(raw)
	if err != nil {
		return nil, "", err
	}
	for redirects := 0; redirects < 5; redirects++ {
		request, _ := http.NewRequest(http.MethodGet, u.String(), nil)
		request.Header.Set("Accept", "image/jpeg,image/png,image/webp,image/gif;q=0.8")
		response, err := s.client.Do(request)
		if err != nil {
			return nil, "", err
		}
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			location := response.Header.Get("Location")
			response.Body.Close()
			if location == "" {
				return nil, "", errors.New("图片重定向缺少目标")
			}
			next, err := u.Parse(location)
			if err != nil {
				return nil, "", err
			}
			u, err = s.checkURL(next.String())
			if err != nil {
				return nil, "", err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return nil, "", fmt.Errorf("图片下载失败：HTTP %d", response.StatusCode)
		}
		if response.ContentLength > s.maxBytes {
			response.Body.Close()
			return nil, "", errors.New("图片超过大小限制")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, s.maxBytes+1))
		response.Body.Close()
		if err != nil {
			return nil, "", err
		}
		if int64(len(data)) > s.maxBytes {
			return nil, "", errors.New("图片超过大小限制")
		}
		return data, strings.Split(response.Header.Get("Content-Type"), ";")[0], nil
	}
	return nil, "", errors.New("图片重定向次数过多")
}

func (s *ImageStore) convert(data []byte) (StoredImage, error) {
	s.sem <- struct{}{}
	defer func() { <-s.sem }()
	var result StoredImage
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return result, errors.New("无法识别图片格式")
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > int64(s.maxPixels) {
		return result, errors.New("图片尺寸或像素数超过限制")
	}
	decoded, err := imaging.Decode(bytes.NewReader(data), imaging.AutoOrientation(true))
	if err != nil {
		return result, err
	}
	if decoded.Bounds().Dx() > 1568 || decoded.Bounds().Dy() > 1568 {
		decoded = imaging.Fit(decoded, 1568, 1568, imaging.Lanczos)
	}
	width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
	if width < 280 || height < 280 {
		canvasWidth, canvasHeight := max(width, 280), max(height, 280)
		canvas := imaging.New(canvasWidth, canvasHeight, color.White)
		decoded = imaging.PasteCenter(canvas, decoded)
	}
	width, height = decoded.Bounds().Dx(), decoded.Bounds().Dy()
	var output bytes.Buffer
	if err = imaging.Encode(&output, decoded, imaging.JPEG, imaging.JPEGQuality(88)); err != nil {
		return result, err
	}
	if output.Len() > 2*1024*1024 {
		output.Reset()
		if err = imaging.Encode(&output, decoded, imaging.JPEG, imaging.JPEGQuality(68)); err != nil {
			return result, err
		}
	}
	sum := sha256.Sum256(output.Bytes())
	fileName := hex.EncodeToString(sum[:]) + ".jpg"
	path := filepath.Join(s.directory, fileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err == nil {
		_, err = file.Write(output.Bytes())
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil && !os.IsExist(err) {
		return result, err
	}
	return StoredImage{FileName: fileName, MIMEType: "image/jpeg", Width: width, Height: height}, nil
}

func (s *ImageStore) Prepare(sources []IncomingImage, limit int) []StoredImage {
	if err := os.MkdirAll(s.directory, 0o750); err != nil {
		log.Printf("创建图片目录失败：%v", err)
		return nil
	}
	result := []StoredImage{}
	if limit > len(sources) {
		limit = len(sources)
	}
	for _, source := range sources[:limit] {
		var data []byte
		var mime string
		var err error
		if source.DataBase64 != "" {
			raw := source.DataBase64
			if comma := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && comma >= 0 {
				raw = raw[comma+1:]
			}
			data, err = base64.StdEncoding.DecodeString(raw)
			mime = source.MIMEType
		} else {
			data, mime, err = s.download(source.URL)
		}
		if err == nil && int64(len(data)) > s.maxBytes {
			err = errors.New("图片超过大小限制")
		}
		var converted StoredImage
		if err == nil {
			converted, err = s.convert(data)
		}
		if err != nil {
			log.Printf("图片处理失败，保留文字消息：%v", err)
			continue
		}
		if converted.Alt = source.Alt; converted.Alt == "" {
			converted.Alt = "博主原始图片"
		}
		converted.SourceURL, converted.SourceMIME = source.URL, mime
		result = append(result, converted)
	}
	return result
}
