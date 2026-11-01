package alerts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codecolors/buddyget/internal/budget"
	"github.com/codecolors/buddyget/internal/forecast"
	"github.com/codecolors/buddyget/internal/ledger"
	"github.com/codecolors/buddyget/internal/platform/calendar"
	"github.com/codecolors/buddyget/internal/platform/db"
	"github.com/codecolors/buddyget/internal/platform/validation"
	"github.com/codecolors/buddyget/internal/recurring"
	"github.com/codecolors/buddyget/internal/savings"
)

const today calendar.Date = "2026-10-31"

// fakeTransport retient les messages au lieu de les envoyer.
type fakeTransport struct {
	sent []Message
	fail error
}

func (f *fakeTransport) email(_ context.Context, _ Channels, m Message) error {
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeTransport) webhook(ctx context.Context, c Channels, m Message) error {
	return f.email(ctx, c, m)
}

type env struct {
	t         testing.TB
	d         *db.DB
	s         *Service
	ledger    *ledger.Service
	recurring *recurring.Service
	savings   *savings.Service
	budgets   *budget.Service
	transport *fakeTransport
}

func newEnv(t testing.TB) *env {
	t.Helper()
	d, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "alerts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	l, r := ledger.NewService(d), recurring.NewService(d)
	sv, b := savings.NewService(d, l), budget.NewService(d, l)
	s := NewService(d, l, r, sv, b, forecast.NewService(d, forecast.Deps{Ledger: l, Recurring: r, Savings: sv, Budgets: b}), "https://budget.example")
	ft := &fakeTransport{}
	s.transport = ft
	return &env{t: t, d: d, s: s, ledger: l, recurring: r, savings: sv, budgets: b, transport: ft}
}

func (e *env) category(name string) int64 {
	e.t.Helper()
	cats, _ := e.ledger.Categories(e.t.Context())
	i := slices.IndexFunc(cats, func(c ledger.Category) bool { return c.FullName() == name })
	if i < 0 {
		e.t.Fatalf("catégorie %q", name)
	}
	return cats[i].ID
}

// situation réunit un cas de chaque type d'alerte au 31/10/2026.
func situation(t *testing.T) *env {
	e := newEnv(t)
	ctx := t.Context()
	courant, err := e.ledger.CreateAccount(ctx, ledger.AccountInput{Name: "Courant", Kind: ledger.AccountCurrent, Bank: ledger.BankSG, OpeningBalance: 1000_00, OpeningDate: "2025-10-01"})
	if err != nil {
		t.Fatal(err)
	}
	livret, _ := e.ledger.CreateAccount(ctx, ledger.AccountInput{Name: "PEL", Kind: ledger.AccountSavings, Bank: ledger.BankSG, OpeningBalance: 500_00, OpeningDate: "2025-10-01"})
	if err := e.savings.Save(ctx, savings.Settings{AccountID: livret, Product: savings.PEL, Method: savings.NoInterest, UnlockDate: "2026-11-15"}, today); err != nil {
		t.Fatal(err)
	}
	loisirs, courses := e.category("Loisirs"), e.category("Alimentation › Courses")
	for _, in := range []budget.Input{{CategoryID: loisirs, Amount: 100_00}, {CategoryID: courses, Amount: 500_00}} {
		if err := e.budgets.Save(ctx, in, "2026-10-01"); err != nil {
			t.Fatal(err)
		}
	}
	ops := []ledger.TransactionInput{
		{Date: "2026-10-20", Label: "Cinéma", Amount: -120_00, CategoryID: loisirs},      // enveloppe dépassée
		{Date: "2026-10-15", Label: "Hypermarché", Amount: -420_00, CategoryID: courses}, // 84 % consommés
		{Date: "2026-10-29", Label: "BOULANGERIE", Amount: -60_00},                       // 12 fois l'habitude
		{Date: "2026-10-10", Label: "NETFLIX", Amount: -19_99},                           // prix relevé
	}
	for _, d := range []calendar.Date{"2026-05-03", "2026-06-03", "2026-07-03", "2026-08-03", "2026-09-03"} {
		ops = append(ops, ledger.TransactionInput{Date: d, Label: "BOULANGERIE", Amount: -5_00})
	}
	for _, in := range ops {
		in.AccountID = courant
		if _, err := e.ledger.CreateTransaction(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []recurring.RuleInput{
		{Label: "NETFLIX", Amount: -17_99, Start: "2026-08-10"},
		{Label: "Loyer", Amount: -2000_00, Start: "2026-01-02"}, // lundi 2 novembre : proche, et sous le seuil
	} {
		in.AccountID, in.Period, in.Shift, in.Origin = courant, calendar.Monthly, calendar.NoShift, recurring.Manual
		if _, err := e.recurring.CreateRule(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.d.W.ExecContext(ctx, `
		INSERT INTO imports (account_id, filename, profile, imported_at, lines_read, lines_added, lines_duplicate, lines_error, lines_skipped)
		VALUES (?, 'releve.csv', 'sg', '2026-10-01T10:00:00Z', 1, 1, 0, 0, 0)`, courant); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEvaluate(t *testing.T) {
	e := situation(t)
	ctx := t.Context()
	n, err := e.s.Evaluate(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	open, _ := e.s.Open(ctx)
	var keys []string
	for _, a := range open {
		keys = append(keys, string(a.Kind)+" "+a.Key)
	}
	slices.Sort(keys)
	loisirs, courses := e.category("Loisirs"), e.category("Alimentation › Courses")
	want := []string{
		"abonnement abonnement:1:4:montant",
		"budget budget:" + strconv.FormatInt(courses, 10) + ":2026-10:risque",
		"budget budget:" + strconv.FormatInt(loisirs, 10) + ":2026-10:depasse",
		"deblocage deblocage:2:2026-11-15",
		"echeance echeance:2:2026-11-02",
		"import import:2026-10-01",
		"inhabituelle inhabituelle:3",
		"prevision prevision:2026-11-02",
	}
	if n != len(want) || !slices.Equal(keys, want) {
		t.Errorf("%d alertes :\n%s\nattendu :\n%s", n, strings.Join(keys, "\n"), strings.Join(want, "\n"))
	}
	for _, a := range open {
		if a.Title == "" || a.Link == "" || a.CreatedOn != today {
			t.Errorf("alerte incomplète : %+v", a)
		}
	}

	// Une situation déjà signalée ne l'est plus, même acquittée.
	if err := e.s.Acknowledge(ctx, open[0].ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.s.Evaluate(ctx, today); n != 0 {
		t.Errorf("examen répété : %d nouvelles alertes", n)
	}
	if c, _ := e.s.OpenCount(ctx); c != len(want)-1 {
		t.Errorf("non acquittées : %d", c)
	}
	if err := e.s.Acknowledge(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if acked, _ := e.s.Acknowledged(ctx, 3); len(acked) != 3 {
		t.Errorf("acquittées : %d", len(acked))
	}
	// Les alertes acquittées anciennes sont purgées.
	if _, err := e.s.Evaluate(ctx, today.AddDays(keepDays+1)); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = e.d.R.QueryRowContext(ctx, `SELECT count(*) FROM alerts WHERE created_on = ?`, today).Scan(&left)
	if left != 0 {
		t.Errorf("%d alertes anciennes conservées", left)
	}
}

func TestSettings(t *testing.T) {
	e := situation(t)
	ctx := t.Context()
	settings, err := e.s.Settings(ctx)
	if err != nil || len(settings) != len(Kinds) || settings[0] != (Setting{BudgetKind, true, 80}) {
		t.Fatalf("réglages par défaut : %+v, %v", settings, err)
	}
	for i := range settings {
		settings[i].Enabled = settings[i].Kind == Unusual
	}
	settings[2].Param = 20 // 60 € n'est plus que 12 fois l'habitude
	if err := e.s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.s.Evaluate(ctx, today); n != 0 {
		t.Errorf("seuil relevé : %d alertes", n)
	}
	settings[2].Param = 3
	_ = e.s.SaveSettings(ctx, settings)
	if n, _ := e.s.Evaluate(ctx, today); n != 1 {
		t.Errorf("seul le type activé : %d alertes", n)
	}
	for _, bad := range []Setting{{BudgetKind, true, 0}, {Unusual, true, 1}, {"inconnu", true, 5}} {
		if _, ok := validation.Message(e.s.SaveSettings(ctx, []Setting{bad})); !ok {
			t.Errorf("réglage invalide accepté : %+v", bad)
		}
	}
	if st, _ := e.s.Setting(ctx, Unusual); st.Param != 3 || !st.Enabled {
		t.Errorf("réglage lu : %+v", st)
	}
}

func TestFlush(t *testing.T) {
	e := situation(t)
	ctx := t.Context()
	if _, err := e.s.Evaluate(ctx, today); err != nil {
		t.Fatal(err)
	}
	// Sans canal : rien n'est envoyé, rien ne le sera après coup.
	if err := e.s.Flush(ctx, today, false); err != nil || len(e.transport.sent) != 0 {
		t.Fatalf("sans canal : %v, %d", err, len(e.transport.sent))
	}

	c, _ := e.s.Channels(ctx)
	if c.Enabled() || !c.Discreet || c.Frequency != Immediate {
		t.Errorf("canaux par défaut : %+v", c)
	}
	c.WebhookEnabled, c.WebhookURL, c.Frequency = true, "https://ntfy.example/budget", Daily
	if err := e.s.SaveChannels(ctx, c); err != nil {
		t.Fatal(err)
	}
	newAlert := func(key string) {
		if _, err := e.s.record(ctx, []Alert{{Kind: Unusual, Key: key, Title: "Dépense inhabituelle : CAVISTE", Detail: "−250,00 €"}}, today); err != nil {
			t.Fatal(err)
		}
	}
	newAlert("essai:1")
	if err := e.s.Flush(ctx, today, false); err != nil || len(e.transport.sent) != 0 {
		t.Errorf("récapitulatif quotidien : rien avant le travail du jour (%d)", len(e.transport.sent))
	}
	if err := e.s.Flush(ctx, today, true); err != nil || len(e.transport.sent) != 1 {
		t.Fatalf("récapitulatif : %v, %d", err, len(e.transport.sent))
	}
	m := e.transport.sent[0]
	if m.Subject != "BuddyGet : 1 nouvelle alerte" || strings.Contains(m.Body, "CAVISTE") || strings.Contains(m.Body, "€") ||
		!strings.Contains(m.Body, "Dépense inhabituelle") || !strings.Contains(m.Body, "https://budget.example/alertes") {
		t.Errorf("notification discrète : %+v", m)
	}

	c.Discreet, c.Frequency = false, Immediate
	_ = e.s.SaveChannels(ctx, c)
	newAlert("essai:2")
	newAlert("essai:3")
	if err := e.s.Flush(ctx, today, false); err != nil || len(e.transport.sent) != 2 {
		t.Fatalf("envoi immédiat : %v, %d", err, len(e.transport.sent))
	}
	if m = e.transport.sent[1]; m.Subject != "BuddyGet : 2 nouvelles alertes" || !strings.Contains(m.Body, "CAVISTE") {
		t.Errorf("notification détaillée : %+v", m)
	}

	// Un échec laisse l'alerte à envoyer.
	e.transport.fail = errors.New("hors ligne")
	newAlert("essai:4")
	if err := e.s.Flush(ctx, today, false); err == nil {
		t.Error("l'échec d'envoi doit être signalé")
	}
	e.transport.fail = nil
	if err := e.s.Flush(ctx, today, false); err != nil || len(e.transport.sent) != 3 {
		t.Errorf("nouvel essai : %v, %d", err, len(e.transport.sent))
	}
	if err := e.s.SendTest(ctx); err != nil || len(e.transport.sent) != 4 {
		t.Errorf("message d'essai : %v", err)
	}
}

func TestChannelsValidation(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	if _, ok := validation.Message(e.s.SendTest(ctx)); !ok {
		t.Error("essai sans canal")
	}
	base, _ := e.s.Channels(ctx)
	for _, mod := range []func(*Channels){
		func(c *Channels) { c.EmailEnabled = true },
		func(c *Channels) {
			c.EmailEnabled, c.SMTPHost, c.EmailFrom, c.EmailTo = true, "smtp.example", "pas une adresse", "a@example.fr"
		},
		func(c *Channels) { c.WebhookEnabled, c.WebhookURL = true, "ftp://ntfy.example" },
		func(c *Channels) { c.Frequency = "souvent" },
	} {
		c := base
		mod(&c)
		if _, ok := validation.Message(e.s.SaveChannels(ctx, c)); !ok {
			t.Errorf("canaux invalides acceptés : %+v", c)
		}
	}
	c := base
	c.EmailEnabled, c.SMTPHost, c.SMTPPort, c.SMTPUser, c.SMTPPassword = true, "smtp.example", 465, "moi", "secret"
	c.SMTPSecurity, c.EmailFrom, c.EmailTo = ImplicitTLS, "BuddyGet <budget@example.fr>", "a@example.fr, b@example.fr"
	if err := e.s.SaveChannels(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.SMTPPassword = "" // inchangé
	if err := e.s.SaveChannels(ctx, c); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.Channels(ctx); got.SMTPPassword != "secret" || got.SMTPPort != 465 {
		t.Errorf("mot de passe conservé : %+v", got)
	}
}

func TestBuildMail(t *testing.T) {
	msg := string(buildMail("budget@example.fr", "a@example.fr", Message{Subject: "Échéance proche", Body: "Ligne 1\nLigne 2\n"},
		time.Date(2026, 10, 31, 8, 0, 0, 0, time.UTC)))
	for _, want := range []string{"Subject: =?utf-8?q?=C3=89ch=C3=A9ance_proche?=\r\n", "Date: Sat, 31 Oct 2026 08:00:00 +0000\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n", "\r\n\r\nLigne 1\r\nLigne 2\r\n"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message : %q absent de\n%s", want, msg)
		}
	}
}

func TestWebhookTransport(t *testing.T) {
	var body, title string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, title = string(b), r.Header.Get("Title")
		if strings.HasSuffix(r.URL.Path, "/refus") {
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c := Channels{WebhookURL: srv.URL + "/budget"}
	if err := (netTransport{}).webhook(t.Context(), c, Message{Subject: "Alerte", Body: "Détail"}); err != nil {
		t.Fatal(err)
	}
	if body != "Détail" || title != "Alerte" {
		t.Errorf("webhook reçu : %q, %q", title, body)
	}
	c.WebhookURL = srv.URL + "/refus"
	if err := (netTransport{}).webhook(t.Context(), c, Message{}); err == nil {
		t.Error("une réponse 403 est un échec")
	}
}

func TestTriggerAndRun(t *testing.T) {
	e := situation(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.s.Run(ctx); close(done) }()
	e.s.Trigger()
	e.s.Trigger() // ne bloque pas
	deadline := time.Now().Add(5 * time.Second)
	for {
		if n, _ := e.s.OpenCount(t.Context()); n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("l'examen demandé n'a pas eu lieu")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}
