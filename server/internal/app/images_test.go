package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"testing"
)

func sourceImage(t *testing.T, width, height int, jpegFormat bool) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{80, 120, 160, 255})
		}
	}
	var output bytes.Buffer
	if jpegFormat {
		_ = jpeg.Encode(&output, img, nil)
	} else {
		_ = png.Encode(&output, img)
	}
	return base64.StdEncoding.EncodeToString(output.Bytes())
}
func TestImagePaddingAndResize(t *testing.T) {
	config := testConfig(t)
	store := NewImageStore(config)
	narrow := store.Prepare([]IncomingImage{{DataBase64: sourceImage(t, 100, 500, false)}}, 1)
	if len(narrow) != 1 || narrow[0].Width != 280 || narrow[0].Height != 500 {
		t.Fatalf("小图留白失败：%+v", narrow)
	}
	large := store.Prepare([]IncomingImage{{DataBase64: sourceImage(t, 2000, 1000, true)}}, 1)
	if len(large) != 1 || large[0].Width != 1568 || large[0].Height != 784 {
		t.Fatalf("大图缩放失败：%+v", large)
	}
	if _, err := os.Stat(config.ImageDir + "/" + large[0].FileName); err != nil {
		t.Fatal("图片未保存")
	}
}
