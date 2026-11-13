package main

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"image"
	"image/color"
	stdDraw "image/draw"
	"image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	_ "golang.org/x/image/webp"
)

//go:embed public
var public embed.FS

//go:embed public/assets/black-aigo.jpg
var blackAigo []byte

//go:embed public/assets/black-asus.jpg
var blackAsus []byte

var blackFrames = map[string][]byte{"aigo": blackAigo, "asus": blackAsus}

type frameSource interface {
	Capture(context.Context, stats, time.Time) ([]byte, error)
	Close()
}

type nativeRenderer struct {
	id                string
	config            screenConfig
	background, frame *image.RGBA
	faces             map[int]font.Face
	key               displayValues
	jpeg              []byte
}

type displayValues struct {
	cpu, temp, gpu, mem, vram                     string
	time, seconds, gpuName, memDetail, vramDetail string
}

var (
	aqua        = color.RGBA{57, 197, 187, 255}
	white       = color.RGBA{234, 255, 251, 255}
	pale        = color.RGBA{179, 245, 228, 255}
	muted       = color.RGBA{173, 221, 206, 255}
	displayFont = sync.OnceValues(loadDisplayFont)
)

func loadDisplayFont() (*opentype.Font, error) {
	fontPath := filepath.Join(os.Getenv("WINDIR"), "Fonts", "msyhbd.ttc")
	if explicit := os.Getenv("SCREEN_FONT"); explicit != "" {
		fontPath = explicit
	}
	fontData, err := os.ReadFile(fontPath)
	if err != nil {
		return nil, fmt.Errorf("load display font: %w", err)
	}
	collection, err := opentype.ParseCollection(fontData)
	if err != nil {
		return nil, err
	}
	return collection.Font(0)
}

func newNativeRenderer(id string, config screenConfig) (*nativeRenderer, error) {
	width, height := dimensions(id)
	r := &nativeRenderer{id: id, config: config, background: image.NewRGBA(image.Rect(0, 0, width, height)), frame: image.NewRGBA(image.Rect(0, 0, width, height)), faces: map[int]font.Face{}}
	typeface, err := displayFont()
	if err != nil {
		return nil, err
	}
	sizes := []int{17, 24, 25, 62}
	if id == "aigo" {
		sizes = []int{13, 16, 17, 20, 26, 32, 56, 92}
	}
	for _, size := range sizes {
		f, err := opentype.NewFace(typeface, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			r.Close()
			return nil, err
		}
		r.faces[size] = f
	}
	if err := r.prepareBackground(); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func (r *nativeRenderer) Close() {
	for _, f := range r.faces {
		_ = f.Close()
	}
}

func fill(dst *image.RGBA, rect image.Rectangle, c color.Color) {
	stdDraw.Draw(dst, rect, image.NewUniform(c), image.Point{}, stdDraw.Over)
}

func translucent(rgb color.RGBA, alpha byte) color.NRGBA {
	return color.NRGBA{rgb.R, rgb.G, rgb.B, alpha}
}

func rounded(dst *image.RGBA, rect image.Rectangle, radius int, c color.Color) {
	if rect.Empty() {
		return
	}
	radius = min(radius, rect.Dx()/2, rect.Dy()/2)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		n := min(y-rect.Min.Y, rect.Max.Y-1-y)
		inset := 0
		if n < radius {
			dy := float64(radius-n) - 0.5
			inset = radius - int(math.Sqrt(float64(radius*radius)-dy*dy)+0.5)
		}
		fill(dst, image.Rect(rect.Min.X+inset, y, rect.Max.X-inset, y+1), c)
	}
}

func outline(dst *image.RGBA, b image.Rectangle, radius int, c color.Color) {
	// An alpha mask keeps the stroke translucent without covering the contents.
	mask := image.NewAlpha(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx := max(b.Min.X+radius-x, x-(b.Max.X-1-radius), 0)
			dy := max(b.Min.Y+radius-y, y-(b.Max.Y-1-radius), 0)
			d := math.Hypot(float64(dx), float64(dy))
			if d > float64(radius) {
				continue
			}
			if x == b.Min.X || x == b.Max.X-1 || y == b.Min.Y || y == b.Max.Y-1 || d > float64(radius-1) {
				mask.SetAlpha(x, y, color.Alpha{255})
			}
		}
	}
	stdDraw.DrawMask(dst, b, image.NewUniform(c), image.Point{}, mask, b.Min, stdDraw.Over)
}

func backgroundImage(name string) (image.Image, error) {
	data, err := public.ReadFile("public/assets/" + name)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func (r *nativeRenderer) prepareBackground() error {
	dst := r.background
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	wide := r.id == "aigo"
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px, py := float64(x)/float64(w), float64(y)/float64(h)
			glow := max(0, 1-math.Hypot(px-.75, py-.3)*1.25)
			if wide {
				glow = max(0, 1-math.Hypot(px-.55, py-.8)*1.4)
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(8 + 34*glow), uint8(47 + 95*glow), uint8(43 + 76*glow), 255})
		}
	}
	if wide {
		art, err := backgroundImage("rurudo-miku-transform.webp")
		if err != nil {
			return err
		}
		aw := 864
		ah := art.Bounds().Dy() * aw / art.Bounds().Dx()
		scaled := image.NewRGBA(image.Rect(0, 0, aw, ah))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), art, art.Bounds(), draw.Src, nil)
		artBounds := image.Rect(1096, -55, 1096+aw, -55+ah)
		mask := image.NewAlpha(image.Rect(0, 0, aw, ah))
		for y := 0; y < ah; y++ {
			for x := 0; x < aw; x++ {
				mask.SetAlpha(x, y, color.Alpha{uint8(min(255, x*255/198))})
			}
		}
		stdDraw.DrawMask(dst, artBounds, scaled, image.Point{}, mask, image.Point{}, stdDraw.Over)
		for x := 0; x < w; x++ {
			a := uint8(max(0, min(207, (1460-x)*207/390)))
			fill(dst, image.Rect(x, 0, x+1, h), color.NRGBA{5, 43, 42, a})
		}
		for x := 0; x < 1320; x += 48 {
			fill(dst, image.Rect(x, 0, x+1, h), color.NRGBA{124, 255, 233, 7})
		}
		for y := 0; y < h; y += 48 {
			fill(dst, image.Rect(0, y, 1320, y+1), color.NRGBA{124, 255, 233, 7})
		}
		outline(dst, image.Rect(23, 15, 1897, 447), 18, color.NRGBA{112, 229, 207, 105})
		rounded(dst, image.Rect(65, 32, 138, 37), 2, aqua)
		for _, x := range []int{65, 471, 876} {
			b := image.Rect(x, 125, x+382, 403)
			rounded(dst, b, 14, color.NRGBA{29, 102, 91, 95})
			outline(dst, b, 14, color.NRGBA{107, 219, 192, 124})
		}
		r.text(dst, 65, 68, 17, pale, "●  MIKU · LIVE SYSTEM")
		r.text(dst, 1711, 428, 13, white, "Art by rurudo · © CFM")
	} else {
		art, err := backgroundImage("rurudo-miku-cutout.png")
		if err != nil {
			return err
		}
		b := art.Bounds()
		w, h := 410, 320
		s := max(float64(w)/float64(b.Dx()), float64(h)/float64(b.Dy()))
		sw, sh := int(float64(b.Dx())*s), int(float64(b.Dy())*s)
		x, y := -5+(w-sw)/2, (h-sh)/2
		draw.CatmullRom.Scale(dst, image.Rect(x, y, x+sw, y+sh), art, b, draw.Over, nil)
		for x := 0; x < 320; x++ {
			a := uint8(max(0, min(240, (235-x)*240/210)))
			fill(dst, image.Rect(x, 0, x+1, 320), color.NRGBA{9, 47, 43, a})
		}
		outline(dst, image.Rect(9, 9, 311, 311), 18, color.NRGBA{85, 230, 219, 168})
		rounded(dst, image.Rect(20, 24, 54, 28), 2, aqua)
		r.text(dst, 20, 62, 17, aqua, "CPU 占用")
		r.text(dst, 20, 188, 17, aqua, "CPU 温度")
	}
	return nil
}

func (r *nativeRenderer) text(dst *image.RGBA, x, y, size int, c color.Color, s string) int {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: r.faces[size], Dot: fixed.P(x, y)}
	d.DrawString(s)
	return d.Dot.X.Round()
}

func number(n *float64) string {
	if n == nil {
		return "--"
	}
	return fmt.Sprintf("%.0f", math.Round(*n))
}

func capacity(used, total *float64) string {
	if used == nil || total == nil || *total <= 0 {
		return "-- / -- GiB"
	}
	return fmt.Sprintf("%.1f / %.1f GiB", *used/(1<<30), *total/(1<<30))
}

func bar(dst *image.RGBA, x, y, width, height int, n *float64, full float64) {
	rounded(dst, image.Rect(x, y, x+width, y+height), height/2, translucent(aqua, 55))
	if n != nil {
		w := int(float64(width) * max(0, min(1, *n/full)))
		rounded(dst, image.Rect(x, y, x+w, y+height), height/2, aqua)
	}
}

func (r *nativeRenderer) Capture(ctx context.Context, s stats, now time.Time) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	k := displayValues{}
	if r.id == "aigo" {
		k.time, k.seconds = now.Format("15:04"), now.Format("05")
		k.gpu, k.mem, k.vram = number(s.GPU), number(s.Memory), number(s.VRAM)
		k.gpuName = s.GPUName
		k.memDetail, k.vramDetail = capacity(s.MemoryUsed, s.MemoryTotal), capacity(s.VRAMUsed, s.VRAMTotal)
	} else {
		k.cpu, k.temp = number(s.CPU), number(s.Temperature)
	}
	if len(r.jpeg) > 0 && r.key == k {
		return r.jpeg, nil
	}
	copy(r.frame.Pix, r.background.Pix)
	d := r.frame
	if r.id == "aigo" {
		r.text(d, 65, 94, 16, muted, defaultString(k.gpuName, "显卡数据暂不可用"))
		x := r.text(d, 1040, 94, 56, white, k.time)
		r.text(d, x+12, 94, 26, pale, k.seconds)
		for i, metric := range []struct {
			label, value, detail string
			percent              *float64
		}{
			{"GPU 占用", k.gpu, "图形处理器负载", s.GPU},
			{"内存占用", k.mem, k.memDetail, s.Memory},
			{"显存占用", k.vram, k.vramDetail, s.VRAM},
		} {
			x := 91 + i*406
			r.text(d, x, 170, 20, pale, metric.label)
			next := r.text(d, x, 275, 92, white, metric.value)
			r.text(d, next+5, 275, 32, aqua, "%")
			bar(d, x, 309, 329, 6, metric.percent, 100)
			r.text(d, x, 353, 17, muted, metric.detail)
		}
	} else {
		x := r.text(d, 20, 128, 62, white, k.cpu)
		r.text(d, x+4, 128, 25, aqua, "%")
		bar(d, 20, 151, 115, 4, s.CPU, 100)
		c := white
		if s.Temperature != nil && *s.Temperature >= 85 {
			c = color.RGBA{255, 188, 160, 255}
		}
		x = r.text(d, 20, 254, 62, c, k.temp)
		r.text(d, x+4, 254, 24, aqua, "°C")
		bar(d, 20, 277, 115, 4, s.Temperature, 95)
	}
	encoded, err := encodeJPEG(d, r.config.Quality, r.config.Rotation, r.id == "asus")
	if err != nil {
		return nil, err
	}
	r.key, r.jpeg = k, encoded
	return encoded, nil
}

func rotateImage(src image.Image, degrees int) image.Image {
	if degrees == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if degrees == 90 || degrees == 270 {
		w, h = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sx, sy int
			switch degrees {
			case 90:
				sx, sy = y, b.Dy()-1-x
			case 180:
				sx, sy = b.Dx()-1-x, b.Dy()-1-y
			case 270:
				sx, sy = b.Dx()-1-y, x
			}
			dst.Set(x, y, src.At(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}

func encodeJPEG(src image.Image, quality, rotation int, asus bool) ([]byte, error) {
	img := rotateImage(src, rotation)
	var out bytes.Buffer
	for q := quality; ; q = max(25, q-10) {
		out.Reset()
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		if !asus || out.Len() <= asusMaxJPEG {
			return out.Bytes(), nil
		}
		if q <= 25 {
			return nil, fmt.Errorf("cannot fit ASUS frame within %d bytes", asusMaxJPEG)
		}
	}
}

func defaultString(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
