package attachments

import (
	"bytes"
	"image/jpeg"
	"testing"
)

// BenchmarkShrink réduit une photo de téléphone de 12 mégapixels
// (4000 × 3000) à 1 600 px de large.
func BenchmarkShrink(b *testing.B) {
	var photo bytes.Buffer
	if err := jpeg.Encode(&photo, halves(4000, 3000), &jpeg.Options{Quality: 90}); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := shrink(photo.Bytes()); err != nil {
			b.Fatal(err)
		}
	}
}
