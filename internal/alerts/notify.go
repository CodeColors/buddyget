package alerts

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/validation"
)

// sendTimeout borne chaque envoi : un serveur injoignable ne bloque rien.
const sendTimeout = 15 * time.Second

// Frequency est le rythme d'envoi des alertes par les canaux sortants.
type Frequency string

const (
	Immediate Frequency = "immediate" // dès qu'elles apparaissent
	Daily     Frequency = "daily"     // un récapitulatif par jour
	Weekly    Frequency = "weekly"    // un récapitulatif le lundi
)

// Frequencies liste les rythmes dans l'ordre d'affichage.
var Frequencies = []Frequency{Immediate, Daily, Weekly}

// Label décrit le rythme.
func (f Frequency) Label() string {
	switch f {
	case Daily:
		return "Récapitulatif quotidien"
	case Weekly:
		return "Récapitulatif hebdomadaire (le lundi)"
	}
	return "Dès qu'une alerte apparaît"
}

// Sécurité de la connexion SMTP.
const (
	StartTLS    = "starttls" // port 587 en général
	ImplicitTLS = "tls"      // port 465 en général
)

// Channels sont les canaux sortants, tous désactivés par défaut.
type Channels struct {
	EmailEnabled   bool
	SMTPHost       string
	SMTPPort       int
	SMTPSecurity   string
	SMTPUser       string
	SMTPPassword   string // jamais réaffiché ; vide à l'enregistrement : inchangé
	EmailFrom      string
	EmailTo        string // une ou plusieurs adresses séparées par des virgules
	WebhookEnabled bool
	WebhookURL     string
	Frequency      Frequency
	Discreet       bool // ni libellé ni montant dans les messages
}

// Enabled indique qu'au moins un canal est activé.
func (c Channels) Enabled() bool { return c.EmailEnabled || c.WebhookEnabled }

// Validate contrôle les canaux activés.
func (c Channels) Validate() error {
	switch {
	case c.Frequency != Immediate && c.Frequency != Daily && c.Frequency != Weekly:
		return validation.Error("Choisissez le rythme d'envoi.")
	case c.SMTPSecurity != StartTLS && c.SMTPSecurity != ImplicitTLS:
		return validation.Error("Choisissez la sécurité de la connexion SMTP.")
	}
	if c.EmailEnabled {
		if strings.TrimSpace(c.SMTPHost) == "" || c.SMTPPort < 1 || c.SMTPPort > 65535 {
			return validation.Error("Indiquez le serveur SMTP et son port.")
		}
		if _, err := mail.ParseAddress(c.EmailFrom); err != nil {
			return validation.Error("Adresse d'expédition invalide.")
		}
		if _, err := mail.ParseAddressList(c.EmailTo); err != nil {
			return validation.Error("Adresse(s) de destination invalide(s).")
		}
	}
	if c.WebhookEnabled {
		u, err := url.Parse(c.WebhookURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return validation.Error("Adresse du webhook invalide : http:// ou https://…")
		}
	}
	return nil
}

// Channels renvoie les canaux sortants.
func (s *Service) Channels(ctx context.Context) (Channels, error) {
	var c Channels
	err := s.db.R.QueryRowContext(ctx, `
		SELECT email_enabled, smtp_host, smtp_port, smtp_security, smtp_user, smtp_password, email_from, email_to,
			webhook_enabled, webhook_url, frequency, discreet
		FROM notification_settings`).Scan(&c.EmailEnabled, &c.SMTPHost, &c.SMTPPort, &c.SMTPSecurity, &c.SMTPUser,
		&c.SMTPPassword, &c.EmailFrom, &c.EmailTo, &c.WebhookEnabled, &c.WebhookURL, &c.Frequency, &c.Discreet)
	return c, err
}

// SaveChannels enregistre les canaux ; un mot de passe vide conserve le
// mot de passe enregistré.
func (s *Service) SaveChannels(ctx context.Context, c Channels) error {
	if err := c.Validate(); err != nil {
		return err
	}
	_, err := s.db.W.ExecContext(ctx, `
		UPDATE notification_settings SET email_enabled = ?, smtp_host = ?, smtp_port = ?, smtp_security = ?, smtp_user = ?,
			smtp_password = CASE WHEN ? = '' THEN smtp_password ELSE ? END, email_from = ?, email_to = ?,
			webhook_enabled = ?, webhook_url = ?, frequency = ?, discreet = ?`,
		c.EmailEnabled, strings.TrimSpace(c.SMTPHost), c.SMTPPort, c.SMTPSecurity, strings.TrimSpace(c.SMTPUser),
		c.SMTPPassword, c.SMTPPassword, strings.TrimSpace(c.EmailFrom), strings.TrimSpace(c.EmailTo),
		c.WebhookEnabled, strings.TrimSpace(c.WebhookURL), c.Frequency, c.Discreet)
	return err
}

// Message est une notification.
type Message struct {
	Subject string
	Body    string
}

// transport achemine les messages ; remplacé dans les tests.
type transport interface {
	email(ctx context.Context, c Channels, m Message) error
	webhook(ctx context.Context, c Channels, m Message) error
}

// Flush envoie les alertes non encore notifiées, selon le rythme choisi :
// à chaque examen (immédiat) ou lors du travail quotidien (daily), chaque
// jour ou le lundi. Sans canal activé, elles sont simplement marquées
// notifiées : activer un canal ne déclenche pas l'envoi de l'historique.
func (s *Service) Flush(ctx context.Context, today calendar.Date, daily bool) error {
	c, err := s.Channels(ctx)
	if err != nil {
		return err
	}
	due := c.Frequency == Immediate || daily && (c.Frequency == Daily || today.Time().Weekday() == time.Monday)
	if c.Enabled() && !due {
		return nil
	}
	pending, err := s.list(ctx, ` WHERE NOT notified AND NOT acknowledged ORDER BY id`)
	if err != nil || len(pending) == 0 {
		return err
	}
	if c.Enabled() {
		if err := s.send(ctx, c, s.compose(pending, c.Discreet)); err != nil {
			return err
		}
	}
	_, err = s.db.W.ExecContext(ctx, `UPDATE alerts SET notified = 1 WHERE NOT notified AND id <= ?`, pending[len(pending)-1].ID)
	return err
}

// SendTest envoie un message d'essai par les canaux enregistrés et activés.
func (s *Service) SendTest(ctx context.Context) error {
	c, err := s.Channels(ctx)
	if err != nil {
		return err
	}
	if !c.Enabled() {
		return validation.Error("Activez et enregistrez d'abord un canal.")
	}
	return s.send(ctx, c, Message{Subject: "BuddyGet : notification d'essai", Body: "Les notifications de BuddyGet arrivent bien par ce canal."})
}

// send utilise chaque canal activé ; il suffit qu'un canal aboutisse, les
// échecs sont journalisés (sans le contenu du message).
func (s *Service) send(ctx context.Context, c Channels, m Message) error {
	var errs []error
	sent := false
	for _, ch := range []struct {
		name    string
		enabled bool
		send    func(context.Context, Channels, Message) error
	}{{"e-mail", c.EmailEnabled, s.transport.email}, {"webhook", c.WebhookEnabled, s.transport.webhook}} {
		if !ch.enabled {
			continue
		}
		if err := ch.send(ctx, c, m); err != nil {
			slog.WarnContext(ctx, "notification non envoyée", "canal", ch.name, "err", err)
			errs = append(errs, validation.Errorf("Envoi par %s impossible : %v", ch.name, err))
			continue
		}
		sent = true
	}
	if sent {
		return nil
	}
	return errors.Join(errs...)
}

// compose rédige le message d'une ou plusieurs alertes ; discret, il ne
// contient que leur nombre et leurs types.
func (s *Service) compose(alerts []Alert, discreet bool) Message {
	var b strings.Builder
	subject := "BuddyGet : " + strconv.Itoa(len(alerts)) + " nouvelles alertes"
	if len(alerts) == 1 {
		subject = "BuddyGet : 1 nouvelle alerte"
		if !discreet {
			subject = "BuddyGet : " + alerts[0].Title
		}
	}
	for _, a := range alerts {
		if discreet {
			b.WriteString("- " + a.Kind.Label() + "\n")
			continue
		}
		b.WriteString("- " + a.Title + "\n")
		if a.Detail != "" {
			b.WriteString("  " + a.Detail + "\n")
		}
	}
	if s.baseURL != "" {
		b.WriteString("\n" + strings.TrimSuffix(s.baseURL, "/") + "/alertes\n")
	}
	return Message{Subject: subject, Body: b.String()}
}

// netTransport envoie réellement les messages.
type netTransport struct{}

// email envoie le message en SMTP, chiffré (STARTTLS obligatoire, ou TLS
// dès la connexion).
func (netTransport) email(ctx context.Context, c Channels, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	tlsConfig := &tls.Config{ServerName: c.SMTPHost, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	if c.SMTPSecurity == ImplicitTLS {
		conn, err = (&tls.Dialer{Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = client.Close() }()
	if c.SMTPSecurity == StartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("le serveur ne propose pas STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if c.SMTPUser != "" {
		if err := client.Auth(smtp.PlainAuth("", c.SMTPUser, c.SMTPPassword, c.SMTPHost)); err != nil {
			return err
		}
	}
	from, _ := mail.ParseAddress(c.EmailFrom)
	to, _ := mail.ParseAddressList(c.EmailTo)
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	for _, a := range to {
		if err := client.Rcpt(a.Address); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMail(c.EmailFrom, c.EmailTo, m, time.Now())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// buildMail rédige un message texte UTF-8 conforme à la RFC 5322.
func buildMail(from, to string, m Message, now time.Time) []byte {
	var b bytes.Buffer
	for _, h := range [][2]string{
		{"From", from},
		{"To", to},
		{"Subject", mime.QEncoding.Encode("utf-8", m.Subject)},
		{"Date", now.Format(time.RFC1123Z)},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "8bit"},
	} {
		fmt.Fprintf(&b, "%s: %s\r\n", h[0], h[1])
	}
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(m.Body, "\n", "\r\n"))
	return b.Bytes()
}

// webhook publie le message en texte brut ; l'en-tête Title est reconnu
// par ntfy, le corps convient aux autres services.
func (netTransport) webhook(ctx context.Context, c Channels, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.WebhookURL, strings.NewReader(m.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Title", mime.QEncoding.Encode("utf-8", m.Subject))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("réponse %d", resp.StatusCode)
	}
	return nil
}
