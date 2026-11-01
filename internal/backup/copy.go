package backup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// copy envoie une archive vers le second emplacement : un dossier monté
// (avec la même rotation des sauvegardes automatiques) ou un dossier
// WebDAV (Nextcloud, serveur WebDAV de DSM…), où la rotation reste à la
// charge du serveur.
func (s *Service) copy(ctx context.Context, st Settings, a Archive) error {
	src := filepath.Join(s.dir, a.Name)
	switch st.Copy {
	case FolderCopy:
		if err := copyFile(src, filepath.Join(st.CopyDir, a.Name)); err != nil {
			return fmt.Errorf("copie vers %s : %w", st.CopyDir, err)
		}
		return rotate(st.CopyDir, st.Keep)
	case WebDAVCopy:
		return s.put(ctx, st, src, a.Name)
	}
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src) //nolint:gosec // G304 : archive de DATA_DIR/backups.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return writeFile(dest, func(out *os.File) error {
		_, err := io.Copy(out, in)
		return err
	})
}

// put dépose l'archive dans le dossier WebDAV.
func (s *Service) put(ctx context.Context, st Settings, src, name string) error {
	f, err := os.Open(src) //nolint:gosec // G304 : archive de DATA_DIR/backups.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimSuffix(st.WebDAVURL, "/")+"/"+name, f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	if st.WebDAVUser != "" {
		req.SetBasicAuth(st.WebDAVUser, st.WebDAVPassword)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("copie WebDAV : %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("copie WebDAV refusée : %s", resp.Status)
	}
	return nil
}
