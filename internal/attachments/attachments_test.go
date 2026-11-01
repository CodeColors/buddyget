package attachments

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

func newService(t *testing.T, maxSize, quota int64) (*Service, int64) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(t.Context(), filepath.Join(dir, "attachments.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l := ledger.NewService(d)
	account, _ := l.CreateAccount(t.Context(), ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningDate: "2026-01-01"})
	tx, err := l.CreateTransaction(t.Context(), ledger.TransactionInput{AccountID: account, Date: "2026-10-31", Label: "Garage", Amount: -250_00})
	if err != nil {
		t.Fatal(err)
	}
	return NewService(d, dir, maxSize, quota), tx
}

// halves dessine une image rouge à gauche, bleue à droite.
func halves(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{0, 0, 255, 255}
			if x < w/2 {
				c = color.RGBA{255, 0, 0, 255}
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// withOrientation insère une orientation EXIF dans un JPEG.
func withOrientation(jpg []byte, orientation byte) []byte {
	tiff := []byte("MM\x00\x2a\x00\x00\x00\x08\x00\x01\x01\x12\x00\x03\x00\x00\x00\x01\x00")
	tiff = append(tiff, orientation, 0, 0, 0, 0, 0, 0)
	seg := append([]byte("Exif\x00\x00"), tiff...)
	app1 := append([]byte{0xFF, 0xE1, byte((len(seg) + 2) >> 8), byte(len(seg) + 2)}, seg...)
	return append(append([]byte{0xFF, 0xD8}, app1...), jpg[2:]...)
}

func decode(t *testing.T, s *Service, a Attachment) image.Image {
	t.Helper()
	data, err := os.ReadFile(s.path(a.File))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestImagesAreShrunk(t *testing.T) {
	s, tx := newService(t, 10<<20, 100<<20)
	ctx := t.Context()
	var big bytes.Buffer
	if err := png.Encode(&big, halves(3200, 1000)); err != nil {
		t.Fatal(err)
	}
	a, err := s.Add(ctx, tx, `C:\Photos\ticket garage.png`, &big)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, s, a)
	if a.Kind != Image || a.Name != "ticket garage.png" || a.ContentType() != "image/jpeg" || img.Bounds().Dx() != 1600 || img.Bounds().Dy() != 500 {
		t.Errorf("image réduite : %+v, %v", a, img.Bounds())
	}

	// Photo prise de côté : l'orientation EXIF 6 la redresse (quart de tour).
	var small bytes.Buffer
	if err := jpeg.Encode(&small, halves(40, 20), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	a, err = s.Add(ctx, tx, "photo.jpg", bytes.NewReader(withOrientation(small.Bytes(), 6)))
	if err != nil {
		t.Fatal(err)
	}
	img = decode(t, s, a)
	r1, _, b1, _ := img.At(10, 5).RGBA()
	r2, _, b2, _ := img.At(10, 35).RGBA()
	if img.Bounds().Dx() != 20 || img.Bounds().Dy() != 40 || r1 < b1 || b2 < r2 {
		t.Errorf("photo redressée : %v, haut %d/%d, bas %d/%d", img.Bounds(), r1, b1, r2, b2)
	}
	if exifOrientation(small.Bytes()) != 1 || exifOrientation([]byte("pas un jpeg")) != 1 {
		t.Error("orientation par défaut")
	}
}

func TestPDFAndLimits(t *testing.T) {
	s, tx := newService(t, 1<<20, 1<<20+100)
	ctx := t.Context()
	pdf := []byte("%PDF-1.4\n" + strings.Repeat("x", 1000))
	a, err := s.Add(ctx, tx, "facture.pdf", bytes.NewReader(pdf))
	if err != nil {
		t.Fatal(err)
	}
	if stored, _ := os.ReadFile(s.path(a.File)); a.Kind != PDF || !bytes.Equal(stored, pdf) || a.Size != int64(len(pdf)) {
		t.Errorf("PDF gardé tel quel : %+v", a)
	}
	for name, content := range map[string][]byte{
		"texte":           []byte("bonjour"),
		"vide":            nil,
		"trop gros":       bytes.Repeat([]byte("%PDF"), 1<<18+1),
		"image illisible": append([]byte("\x89PNG\r\n\x1a\n"), "abîmée"...),
	} {
		if _, err := s.Add(ctx, tx, name, bytes.NewReader(content)); err == nil {
			t.Errorf("%s : accepté", name)
		} else if _, ok := validation.Message(err); !ok {
			t.Errorf("%s : %v", name, err)
		}
	}
	// Le quota est presque atteint par le premier PDF.
	if _, err := s.Add(ctx, tx, "deuxième.pdf", bytes.NewReader(append([]byte("%PDF-1.4\n"), make([]byte, 1<<20-500)...))); err == nil ||
		!strings.Contains(err.Error(), "Espace des pièces jointes plein") {
		t.Errorf("quota : %v", err)
	}
	if _, err := s.Add(ctx, 999, "x.pdf", bytes.NewReader(pdf)); !errors.Is(err, ErrNotFound) {
		t.Errorf("opération inconnue : %v", err)
	}

	list, _ := s.List(ctx, tx)
	got, f, err := s.Open(ctx, a.ID)
	if err != nil || len(list) != 1 || got.Name != "facture.pdf" {
		t.Fatalf("lecture : %+v, %v", list, err)
	}
	_ = f.Close()

	// Fichier orphelin (opération supprimée) : nettoyé.
	if err := os.WriteFile(s.path("orphelin.jpg"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Sweep(ctx); err != nil || n != 1 {
		t.Errorf("nettoyage : %d, %v", n, err)
	}
	if err := s.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.path(a.File)); !os.IsNotExist(err) {
		t.Error("fichier supprimé avec la pièce jointe")
	}
	if err := s.Delete(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("suppression répétée : %v", err)
	}
	if displayName("  ") != "pièce jointe" || len([]rune(displayName(strings.Repeat("é", 200)))) != nameMaxLen {
		t.Error("nom affiché")
	}
}
