package main

import (
	"bytes"
	"image"
	"image/jpeg"
	_ "image/png"
)

// decodeImage 解码 PNG/JPEG。
func decodeImage(b []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}

// rotateCW 顺时针旋转 deg 度(deg 取 0/90/180/270),返回新图。
func rotateCW(src image.Image, deg int) image.Image {
	deg = ((deg % 360) + 360) % 360
	if deg == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	switch deg {
	case 90:
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 180:
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(w-1-x, h-1-y, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	case 270:
		dst := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return dst
	}
	return src
}

// downscale 等比缩小到长边不超过 maxDim(最近邻,够用于方向判定/缩小传输)。
func downscale(src image.Image, maxDim int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	long := w
	if h > long {
		long = h
	}
	if long <= maxDim {
		return src
	}
	scale := float64(maxDim) / float64(long)
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := b.Min.Y + int(float64(y)/scale)
		for x := 0; x < nw; x++ {
			sx := b.Min.X + int(float64(x)/scale)
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// normalizeUpload 上传时按 EXIF 朝向把像素转正,再统一编码为 JPEG 存储。
// 这是"方向校正"的工程部分(确定性);解码后即丢弃 EXIF,避免重复应用。
func normalizeUpload(raw []byte) []byte {
	img, err := decodeImage(raw)
	if err != nil {
		return raw // 解码不了(如 HEIC),原样存,交给老师手动处理
	}
	if deg := exifOrientationCW(raw); deg != 0 {
		img = rotateCW(img, deg)
	}
	out, err := encodeJPEG(img, 92)
	if err != nil {
		return raw
	}
	return out
}

// exifOrientationCW 从 JPEG 的 EXIF 读取朝向,返回让图正立需顺时针旋转的角度(0/90/180/270)。
// 仅处理常见的 1/3/6/8;镜像值或无 EXIF 返回 0。
func exifOrientationCW(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 { // 非 JPEG
		return 0
	}
	i := 2
	for i+4 < len(b) {
		if b[i] != 0xFF {
			break
		}
		marker := b[i+1]
		if marker == 0xD9 || marker == 0xDA { // EOI / 图像数据开始
			break
		}
		size := int(b[i+2])<<8 | int(b[i+3])
		if size < 2 || i+2+size > len(b) {
			break
		}
		if marker == 0xE1 { // APP1(EXIF)
			if o := parseExifSeg(b[i+4 : i+2+size]); o != 0 {
				return o
			}
		}
		i += 2 + size
	}
	return 0
}

func parseExifSeg(seg []byte) int {
	if len(seg) < 14 || string(seg[0:4]) != "Exif" {
		return 0
	}
	tiff := seg[6:] // 跳过 "Exif\0\0"
	if len(tiff) < 8 {
		return 0
	}
	var be bool
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		be = false
	case tiff[0] == 'M' && tiff[1] == 'M':
		be = true
	default:
		return 0
	}
	u16 := func(p int) int {
		if p < 0 || p+2 > len(tiff) {
			return -1
		}
		if be {
			return int(tiff[p])<<8 | int(tiff[p+1])
		}
		return int(tiff[p+1])<<8 | int(tiff[p])
	}
	u32 := func(p int) int {
		if p < 0 || p+4 > len(tiff) {
			return -1
		}
		if be {
			return int(tiff[p])<<24 | int(tiff[p+1])<<16 | int(tiff[p+2])<<8 | int(tiff[p+3])
		}
		return int(tiff[p+3])<<24 | int(tiff[p+2])<<16 | int(tiff[p+1])<<8 | int(tiff[p])
	}
	ifd := u32(4)
	n := u16(ifd)
	if n < 0 {
		return 0
	}
	for k := 0; k < n; k++ {
		e := ifd + 2 + k*12
		if e+12 > len(tiff) {
			break
		}
		if u16(e) == 0x0112 { // Orientation 标签
			switch u16(e + 8) { // SHORT 值就存在条目的值字段里
			case 3:
				return 180
			case 6:
				return 90
			case 8:
				return 270
			default:
				return 0
			}
		}
	}
	return 0
}

// encodeJPEG 把图编码为 JPEG(缩小传输体积、加快模型调用)。
func encodeJPEG(img image.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
