package preview

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // register decoder
	_ "image/jpeg" // register decoder
	_ "image/png"  // register decoder
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	_ "golang.org/x/image/bmp"  // register decoder
	_ "golang.org/x/image/tiff" // register decoder
	_ "golang.org/x/image/webp" // register decoder

	"github.com/abunjevac/bfiles/internal/vfs"
)

// maxImageBytes bounds how much is read to decode an image.
const maxImageBytes = 64 << 20

func imagePreview(ctx context.Context, req Request) Result {
	r, err := req.FS.Open(ctx, req.Entry.Path)
	if err != nil {
		return errorResult(err)
	}

	defer r.Close()

	img, format, err := image.Decode(io.LimitReader(r, maxImageBytes))
	if err != nil {
		return errorResult(fmt.Errorf("decode image: %w", err))
	}

	b := img.Bounds()

	return Result{
		Kind:  KindImage,
		Title: fmt.Sprintf("%s · %d×%d", format, b.Dx(), b.Dy()),
		Lines: halfBlocks(img, req.Width, req.Height),
	}
}

// halfBlocks draws img into width×height cells. Each cell shows two vertical pixels:
// the upper half block takes the top pixel as foreground, the bottom as background.
func halfBlocks(img image.Image, width, height int) []string {
	b := img.Bounds()

	if b.Dx() == 0 || b.Dy() == 0 || width <= 0 || height <= 0 {
		return nil
	}

	// cells are roughly twice as tall as wide, which the half blocks compensate
	scale := min(float64(width)/float64(b.Dx()), float64(height*2)/float64(b.Dy()))
	scale = min(scale, 1)

	w := max(int(float64(b.Dx())*scale), 1)
	h := max(int(float64(b.Dy())*scale), 2)

	sample := func(x, y int) color.RGBA {
		// box-average the source area of one output pixel
		x0 := b.Min.X + x*b.Dx()/w
		x1 := max(b.Min.X+(x+1)*b.Dx()/w, x0+1)
		y0 := b.Min.Y + y*b.Dy()/h
		y1 := max(b.Min.Y+(y+1)*b.Dy()/h, y0+1)

		step := max((x1-x0)/4, 1)

		var rs, gs, bs, n uint32

		for sy := y0; sy < y1; sy += max((y1-y0)/4, 1) {
			for sx := x0; sx < x1; sx += step {
				// RGBA is alpha-premultiplied, i.e. already composited on black
				r, g, bb, _ := img.At(sx, sy).RGBA()

				rs += r >> 8
				gs += g >> 8
				bs += bb >> 8
				n++
			}
		}

		return color.RGBA{uint8(rs / n), uint8(gs / n), uint8(bs / n), 255} //nolint:gosec // averages of 8-bit samples stay within 0..255
	}

	lines := make([]string, 0, (h+1)/2)

	for y := 0; y < h; y += 2 {
		var sb strings.Builder

		for x := range w {
			top := sample(x, y)
			bottom := top

			if y+1 < h {
				bottom = sample(x, y+1)
			}

			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm▀", top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
		}

		sb.WriteString("\x1b[0m")
		lines = append(lines, sb.String())
	}

	return lines
}

// videoPreview renders a thumbnail through ffmpegthumbnailer when it is installed and
// the file is local.
func videoPreview(ctx context.Context, req Request) (Result, bool) {
	l, ok := req.FS.(vfs.Localizer)
	if !ok {
		return Result{}, false
	}

	src, ok := l.LocalPath(req.Entry.Path)
	if !ok {
		return Result{}, false
	}

	bin, err := exec.LookPath("ffmpegthumbnailer")
	if err != nil {
		return Result{}, false
	}

	dir, err := os.MkdirTemp("", "bfiles-thumb-*")
	if err != nil {
		return Result{}, false
	}

	defer os.RemoveAll(dir)

	out := filepath.Join(dir, "thumb.png")

	cmd := exec.CommandContext(ctx, bin, "-i", src, "-o", out, "-s", strconv.Itoa(max(req.Width*2, 128)))
	if err = cmd.Run(); err != nil {
		return Result{}, false
	}

	f, err := os.Open(out)
	if err != nil {
		return Result{}, false
	}

	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return Result{}, false
	}

	return Result{Kind: KindImage, Title: "video thumbnail", Lines: halfBlocks(img, req.Width, req.Height)}, true
}
