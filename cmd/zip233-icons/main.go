// Command zip233-icons generates every zip233 icon artifact.
//
// It is pure Go and has no external tool or font dependencies. The master is
// drawn procedurally in a Vercel-inspired style: a geometric white triangle on
// pure black, a monospaced "Z" glyph, and restrained greyscale hairlines. Run
// it from anywhere:
//
//	go run ./cmd/zip233-icons
//
// The generator is idempotent: every run rewrites all output files from the
// freshly drawn master (or from -source when an external override is given),
// so re-running it never accumulates stale artifacts.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
)

const (
	// masterSize is the internal drawing resolution. It exceeds 1024 so that
	// downsampling to every target size yields clean antialiased edges.
	masterSize = 2048
)

var (
	// pngSizes are the standalone transparent PNG outputs.
	pngSizes = []int{16, 24, 32, 48, 64, 128, 256, 512, 1024}

	// icoSizes are embedded into the Windows ICO container.
	icoSizes = []int{16, 24, 32, 48, 64, 128, 256}

	// menuSizes are the standalone right-click-menu logo PNG outputs.
	menuSizes = []int{16, 24, 32, 48, 64, 128, 256}

	// icnsTypes maps macOS ICNS OSType codes to their pixel sizes.
	icnsTypes = []struct {
		typ  string
		size int
	}{
		{"icp4", 16},
		{"icp5", 32},
		{"icp6", 64},
		{"ic07", 128},
		{"ic08", 256},
		{"ic09", 512},
		{"ic10", 1024},
	}
)

// Vercel-style palette: pure black / pure white with two restrained greys for
// secondary line work. No colour gradients, no rounded cartoon shapes.
var (
	cBlack    = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xFF}
	cWhite    = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	cDotGrey  = color.NRGBA{R: 0x2A, G: 0x2A, B: 0x2A, A: 0xFF}
	cLineGrey = color.NRGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xFF}
)

func main() {
	sourceFlag := flag.String("source", "", "外部母版 PNG 路径（留空则使用内置 Vercel 风格代码母版）")
	outFlag := flag.String("out", "", "输出目录（默认 resources/icons）")
	flag.Parse()

	root := findModuleRoot()
	iconDir := filepath.Join(root, "resources", "icons")
	if *outFlag != "" {
		iconDir = *outFlag
	}
	if err := os.MkdirAll(iconDir, 0o755); err != nil {
		exitf("无法创建输出目录 %s: %v", iconDir, err)
	}

	// Built-in master by default; -source only overrides the main artwork.
	builtin := *sourceFlag == ""
	var master image.Image
	if builtin {
		master = drawMaster(false)
		fmt.Printf("母版: 内置 Vercel 代码母版 (%dx%d)\n", masterSize, masterSize)
		// Keep every image in the repository on-style: replace the obsolete
		// coloured master with the newly drawn one.
		sourcePath := filepath.Join(iconDir, "zip233-source.png")
		if _, err := writePNG(sourcePath, renderScaled(master, 1024)); err != nil {
			exitf("写入 %s 失败: %v", sourcePath, err)
		}
		fmt.Printf("  PNG  zip233-source.png (1024x1024)\n")
	} else {
		src, err := loadPNG(*sourceFlag)
		if err != nil {
			exitf("无法读取母版 %s: %v", *sourceFlag, err)
		}
		master = src
		fmt.Printf("母版: %s (%dx%d)\n", *sourceFlag, src.Bounds().Dx(), src.Bounds().Dy())
	}

	// 1. Standalone PNGs + byte cache for the containers.
	pngBySize := make(map[int][]byte, len(pngSizes))
	for _, size := range pngSizes {
		img := renderScaled(master, size)
		path := filepath.Join(iconDir, fmt.Sprintf("zip233-%d.png", size))
		data, err := writePNG(path, img)
		if err != nil {
			exitf("写入 %s 失败: %v", path, err)
		}
		pngBySize[size] = data
		fmt.Printf("  PNG  zip233-%d.png (%dx%d)\n", size, size, size)
	}

	// 2. Multi-resolution Windows ICO (PNG-compressed entries).
	icoBytes := make(map[int][]byte, len(icoSizes))
	for _, size := range icoSizes {
		icoBytes[size] = pngBySize[size]
	}
	icoPath := filepath.Join(iconDir, "zip233.ico")
	if err := writeICO(icoPath, icoSizes, icoBytes); err != nil {
		exitf("写入 %s 失败: %v", icoPath, err)
	}
	fmt.Printf("  ICO  zip233.ico (%d 个分辨率)\n", len(icoSizes))

	// 3. macOS ICNS container.
	icnsPath := filepath.Join(iconDir, "zip233.icns")
	if err := writeICNS(icnsPath, pngBySize); err != nil {
		exitf("写入 %s 失败: %v", icnsPath, err)
	}
	fmt.Printf("  ICNS zip233.icns (%d 个条目)\n", len(icnsTypes))

	// 4. Right-click-menu logo: an inverted badge (white ground, black
	//    triangle, heavier white Z) so it reads clearly at 16px and is
	//    visually distinct from the main artwork.
	menuMaster := drawMaster(true)
	menuPNG := make(map[int][]byte, len(menuSizes))
	for _, size := range menuSizes {
		img := renderScaled(menuMaster, size)
		path := filepath.Join(iconDir, fmt.Sprintf("zip233-menu-%d.png", size))
		data, err := writePNG(path, img)
		if err != nil {
			exitf("写入 %s 失败: %v", path, err)
		}
		menuPNG[size] = data
		fmt.Printf("  PNG  zip233-menu-%d.png (%dx%d)\n", size, size, size)
	}
	menuIcoPath := filepath.Join(iconDir, "zip233-menu.ico")
	if err := writeICO(menuIcoPath, icoSizes, menuPNG); err != nil {
		exitf("写入 %s 失败: %v", menuIcoPath, err)
	}
	fmt.Printf("  ICO  zip233-menu.ico (%d 个分辨率)\n", len(icoSizes))

	// 5. Vector twin of the menu badge, same black/white Vercel geometry.
	svgPath := filepath.Join(iconDir, "zip233-menu.svg")
	if err := os.WriteFile(svgPath, []byte(menuSVG), 0o644); err != nil {
		exitf("写入 %s 失败: %v", svgPath, err)
	}
	fmt.Printf("  SVG  zip233-menu.svg\n")

	fmt.Printf("全部图标已写入 %s\n", iconDir)
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "错误: "+format+"\n", args...)
	os.Exit(1)
}

// findModuleRoot walks up from the working directory looking for our go.mod.
func findModuleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && bytes.Contains(data, []byte("github.com/neko233-com/zip233")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

func loadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// drawMaster renders the Vercel-inspired master procedurally.
//
//   - menu=false: black ground, white triangle, black monospaced "Z", with
//     subtle greyscale dot grid and hairlines in the negative space.
//   - menu=true: inverted badge — white ground, black triangle and a heavier
//     white "Z" — with no secondary ornament so it stays legible at 16px.
//
// All geometry is normalised to masterSize, so both variants share the exact
// same proportions.
func drawMaster(menu bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, masterSize, masterSize))
	W := float64(masterSize)

	ground, triangle, glyph := cBlack, cWhite, cBlack
	if menu {
		ground, triangle, glyph = cWhite, cBlack, cWhite
	}
	fillRectF(img, 0, 0, W, W, ground)

	// Equilateral triangle, apex up, occupying the central 62% of the canvas.
	apexY := 0.18 * W
	baseY := 0.80 * W
	cx := 0.50 * W
	halfBase := (baseY - apexY) / math.Sqrt(3)
	fillTriangle(img, cx, apexY, cx-halfBase, baseY, cx+halfBase, baseY, triangle)

	// Monospaced "Z" assembled from three solids: top bar, bottom bar and a
	// diagonal quad. No font or glyph rasterizer involved. The menu glyph is
	// deliberately heavier for small-size legibility.
	stroke := 0.085 * W
	if menu {
		stroke = 0.105 * W
	}
	zTop, zBot := 0.40*W, 0.70*W
	zLeft, zRight := 0.39*W, 0.61*W
	drawZ(img, zLeft, zTop, zRight, zBot, stroke, glyph)

	if !menu {
		// Restrained greyscale secondary detail in the negative space.
		for _, fx := range []float64{0.08, 0.13, 0.18, 0.82, 0.87, 0.92} {
			for _, fy := range []float64{0.22, 0.32, 0.42, 0.52, 0.62, 0.72, 0.82} {
				d := 0.010 * W
				fillRectF(img, fx*W-d/2, fy*W-d/2, fx*W+d/2, fy*W+d/2, cDotGrey)
			}
		}
		for _, fy := range []float64{0.885, 0.915, 0.945} {
			fillRectF(img, 0.40*W, fy*W, 0.60*W, (fy+0.004)*W, cLineGrey)
		}
	}
	return img
}

// fillRectF fills an axis-aligned rectangle with a solid colour.
func fillRectF(img *image.NRGBA, x0, y0, x1, y1 float64, col color.NRGBA) {
	b := img.Bounds()
	ix0 := int(math.Max(float64(b.Min.X), math.Floor(x0)))
	iy0 := int(math.Max(float64(b.Min.Y), math.Floor(y0)))
	ix1 := int(math.Min(float64(b.Max.X), math.Ceil(x1)))
	iy1 := int(math.Min(float64(b.Max.Y), math.Ceil(y1)))
	for y := iy0; y < iy1; y++ {
		py := float64(y) + 0.5
		if py < y0 || py >= y1 {
			continue
		}
		for x := ix0; x < ix1; x++ {
			px := float64(x) + 0.5
			if px < x0 || px >= x1 {
				continue
			}
			img.SetNRGBA(x, y, col)
		}
	}
}

// fillTriangle fills a solid triangle using consistent cross-product signs.
func fillTriangle(img *image.NRGBA, ax, ay, bx, by, cx, cy float64, col color.NRGBA) {
	minX := math.Max(0, math.Floor(math.Min(ax, math.Min(bx, cx))))
	minY := math.Max(0, math.Floor(math.Min(ay, math.Min(by, cy))))
	maxX := math.Min(float64(img.Bounds().Dx()), math.Ceil(math.Max(ax, math.Max(bx, cx))))
	maxY := math.Min(float64(img.Bounds().Dy()), math.Ceil(math.Max(ay, math.Max(by, cy))))

	area := (bx-ax)*(cy-ay) - (by-ay)*(cx-ax)
	if area == 0 {
		return
	}
	sign := 1.0
	if area < 0 {
		sign = -1
	}
	for y := int(minY); y < int(maxY); y++ {
		py := float64(y) + 0.5
		for x := int(minX); x < int(maxX); x++ {
			px := float64(x) + 0.5
			d1 := (bx-ax)*(py-ay) - (by-ay)*(px-ax)
			d2 := (cx-bx)*(py-by) - (cy-by)*(px-bx)
			d3 := (ax-cx)*(py-cy) - (ay-cy)*(px-cx)
			if sign*d1 >= 0 && sign*d2 >= 0 && sign*d3 >= 0 {
				img.SetNRGBA(x, y, col)
			}
		}
	}
}

// fillQuad fills a convex quadrilateral as two triangles.
func fillQuad(img *image.NRGBA, ax, ay, bx, by, cx, cy, dx, dy float64, col color.NRGBA) {
	fillTriangle(img, ax, ay, bx, by, cx, cy, col)
	fillTriangle(img, ax, ay, cx, cy, dx, dy, col)
}

// drawZ draws a geometric, monospaced "Z": two horizontal bars joined by a
// solid diagonal band.
func drawZ(img *image.NRGBA, x0, y0, x1, y1, stroke float64, col color.NRGBA) {
	fillRectF(img, x0, y0, x1, y0+stroke, col)
	fillRectF(img, x0, y1-stroke, x1, y1, col)

	// Diagonal band from the top-right bar to the bottom-left bar. Offset the
	// centre line by half the stroke along its normal to get a constant width.
	sx, sy := x1, y0+stroke/2
	ex, ey := x0, y1-stroke/2
	dx, dy := ex-sx, ey-sy
	length := math.Hypot(dx, dy)
	if length == 0 {
		return
	}
	nx, ny := -dy/length*stroke/2, dx/length*stroke/2
	fillQuad(img,
		sx+nx, sy+ny,
		sx-nx, sy-ny,
		ex-nx, ey-ny,
		ex+nx, ey+ny,
		col,
	)
}

// renderScaled downsamples src to an exact square using CatmullRom.
func renderScaled(src image.Image, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

func writePNG(path string, img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeICO writes an ICO directory followed by PNG-compressed images.
// Modern Windows accepts PNG payloads in ICO entries (Vista and newer).
func writeICO(path string, sizes []int, images map[int][]byte) error {
	var buf bytes.Buffer
	putU16LE(&buf, 0) // reserved
	putU16LE(&buf, 1) // type: icon
	putU16LE(&buf, uint16(len(sizes)))

	offset := 6 + 16*len(sizes)
	for _, size := range sizes {
		data := images[size]
		if len(data) == 0 {
			return fmt.Errorf("缺少 ICO 尺寸 %d 的图像数据", size)
		}
		dim := uint8(size)
		if size >= 256 {
			dim = 0 // 0 means 256 in the ICO directory
		}
		buf.WriteByte(dim) // width
		buf.WriteByte(dim) // height
		buf.WriteByte(0)   // color count
		buf.WriteByte(0)   // reserved
		putU16LE(&buf, 1)  // color planes
		putU16LE(&buf, 32) // bits per pixel
		putU32LE(&buf, uint32(len(data)))
		putU32LE(&buf, uint32(offset))
		offset += len(data)
	}
	for _, size := range sizes {
		buf.Write(images[size])
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// writeICNS writes an ICNS container with PNG entries.
func writeICNS(path string, images map[int][]byte) error {
	var body bytes.Buffer
	for _, entry := range icnsTypes {
		data := images[entry.size]
		if len(data) == 0 {
			return fmt.Errorf("缺少 ICNS 尺寸 %d 的图像数据", entry.size)
		}
		if len(entry.typ) != 4 {
			return fmt.Errorf("非法的 ICNS 类型 %q", entry.typ)
		}
		body.WriteString(entry.typ)
		putU32BE(&body, uint32(len(data)+8))
		body.Write(data)
	}

	var out bytes.Buffer
	out.WriteString("icns")
	putU32BE(&out, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return os.WriteFile(path, out.Bytes(), 0o644)
}

func putU16LE(buf *bytes.Buffer, v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	buf.Write(b[:])
}

func putU32LE(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

func putU32BE(buf *bytes.Buffer, v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	buf.Write(b[:])
}

// menuSVG is the vector twin of the menu badge: inverted black triangle on
// white with a heavier white Z. Geometry mirrors drawMaster(true) scaled to
// a 512x512 viewBox.
const menuSVG = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512" role="img" aria-label="zip233 menu logo">
  <rect width="512" height="512" fill="#FFFFFF"/>
  <polygon points="256,92 439,410 73,410" fill="#000000"/>
  <g fill="#FFFFFF">
    <rect x="200" y="205" width="112" height="44"/>
    <rect x="200" y="315" width="112" height="44"/>
    <polygon points="312,205 312,249 200,358 200,314"/>
  </g>
</svg>
`
