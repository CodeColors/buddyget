package attachments

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	_ "image/gif" // décodeurs enregistrés auprès d'image.Decode
	"image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/codecolors/buddyget/internal/platform/validation"
)

// Réduction des images.
const (
	maxWidth    = 1600       // largeur maximale conservée, en pixels
	jpegQuality = 75         // qualité JPEG
	maxPixels   = 40_000_000 // au-delà, l'image est refusée (bombe de décompression)
)

// shrink décode une image, la redresse selon son orientation EXIF, la
// réduit à maxWidth pixels de large et la recompresse en JPEG.
func shrink(data []byte) ([]byte, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, validation.Error("Image illisible.")
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, validation.Error("Image trop grande : 40 millions de pixels au plus.")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, validation.Error("Image illisible.")
	}
	orientation := exifOrientation(data)
	// Largeur affichée : celle de l'image redressée.
	b := src.Bounds()
	shown := b.Dx()
	if orientation >= 5 {
		shown = b.Dy()
	}
	w, h := b.Dx(), b.Dy()
	if shown > maxWidth {
		w, h = max(w*maxWidth/shown, 1), max(h*maxWidth/shown, 1)
	}
	// Réduction en deux temps, économe en mémoire : moyenne de blocs de
	// k × k pixels (qui évite le crénelage), puis interpolation bilinéaire
	// sans tampon intermédiaire pour le facteur restant, inférieur à 2. Les
	// noyaux de x/image/draw alloueraient, eux, un tampon de largeur cible ×
	// hauteur source en flottants (150 Mo pour une photo de 12 Mpx).
	from := src
	if k := shown / maxWidth; k >= 2 {
		from = boxShrink(src, k)
	}
	// Fond blanc : la transparence n'existe pas en JPEG.
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), from, from.Bounds(), draw.Over, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, orient(dst, orientation), &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// boxShrink réduit une image d'un facteur entier k en moyennant chaque bloc
// de k × k pixels. Les photos JPEG (YCbCr) sont lues sans allocation.
func boxShrink(src image.Image, k int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx()/k, b.Dy()/k
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	pixel := func(x, y int) (r, g, bl, a uint32) { return src.At(x, y).RGBA() }
	if ycc, ok := src.(*image.YCbCr); ok {
		pixel = func(x, y int) (uint32, uint32, uint32, uint32) {
			c := ycc.YCbCrAt(x, y)
			r, g, bl := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
			return uint32(r) << 8, uint32(g) << 8, uint32(bl) << 8, 0xFFFF
		}
	}
	n := uint32(k * k) //nolint:gosec // G115 : k vaut au plus quelques dizaines.
	for y := range h {
		for x := range w {
			var r, g, bl, a uint32
			for dy := range k {
				for dx := range k {
					cr, cg, cb, ca := pixel(b.Min.X+x*k+dx, b.Min.Y+y*k+dy)
					r, g, bl, a = r+cr, g+cg, bl+cb, a+ca
				}
			}
			i := dst.PixOffset(x, y)
			// Moyennes sur 16 bits ramenées à 8 : jamais plus de 255.
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), uint8(a/n>>8) //nolint:gosec // G115
		}
	}
	return dst
}

// orient applique une orientation EXIF (2 à 8) à une image.
func orient(src *image.RGBA, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch orientation {
			case 2: // miroir horizontal
				dx, dy = w-1-x, y
			case 3: // demi-tour
				dx, dy = w-1-x, h-1-y
			case 4: // miroir vertical
				dx, dy = x, h-1-y
			case 5: // transposition
				dx, dy = y, x
			case 6: // quart de tour horaire
				dx, dy = h-1-y, x
			case 7: // transposition inverse
				dx, dy = h-1-y, w-1-x
			case 8: // quart de tour antihoraire
				dx, dy = y, w-1-x
			}
			dst.SetRGBA(dx, dy, src.RGBAAt(x, y))
		}
	}
	return dst
}

// exifOrientation lit l'orientation (balise 0x0112) d'un JPEG ; 1 (normale)
// si elle est absente ou illisible.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data) && data[i] == 0xFF; {
		marker, size := data[i+1], int(binary.BigEndian.Uint16(data[i+2:]))
		if marker == 0xDA || size < 2 || i+2+size > len(data) { // début de l'image
			break
		}
		if seg := data[i+4 : i+2+size]; marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return tiffOrientation(seg[6:])
		}
		i += 2 + size
	}
	return 1
}

// tiffOrientation cherche l'orientation dans le premier répertoire TIFF.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	ifd := int(order.Uint32(t[4:]))
	if ifd+2 > len(t) {
		return 1
	}
	n := int(order.Uint16(t[ifd:]))
	for e := range n {
		at := ifd + 2 + 12*e
		if at+12 > len(t) {
			break
		}
		if order.Uint16(t[at:]) == 0x0112 {
			if v := int(order.Uint16(t[at+8:])); v >= 1 && v <= 8 {
				return v
			}
		}
	}
	return 1
}
