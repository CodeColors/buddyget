package web

import (
	"net/http"
	"strconv"

	"github.com/codecolors/buddyget/internal/alerts"
)

// recentAcknowledged est le nombre d'alertes acquittées rappelées.
const recentAcknowledged = 20

// alertsView est la donnée de la page des alertes.
type alertsView struct {
	Open         []alerts.Alert
	Acknowledged []alerts.Alert
}

func (s *Server) alertsPage(w http.ResponseWriter, r *http.Request) {
	var v alertsView
	var err error
	if v.Open, err = s.alerts.Open(r.Context()); err == nil {
		v.Acknowledged, err = s.alerts.Acknowledged(r.Context(), recentAcknowledged)
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "alerts", "Alertes", v)
}

// acknowledgeAlert acquitte une alerte, ou toutes (id = 0).
func (s *Server) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	id := formID(r, "id")
	if err := s.alerts.Acknowledge(r.Context(), id); err != nil {
		s.serverError(w, r, err)
		return
	}
	msg := "Alerte acquittée."
	if id == 0 {
		msg = "Toutes les alertes sont acquittées."
	}
	s.redirect(w, r, "/alertes", msg)
}

// alertSettingsView est la donnée de la page des réglages des alertes.
type alertSettingsView struct {
	Settings    []alerts.Setting
	Channels    alerts.Channels
	Frequencies []alerts.Frequency
	Error       string
}

func (s *Server) alertSettingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderAlertSettings(w, r, http.StatusOK, nil, nil, "")
}

// renderAlertSettings affiche les réglages enregistrés, ou la saisie
// refusée (settings ou channels non nuls) avec son message.
func (s *Server) renderAlertSettings(w http.ResponseWriter, r *http.Request, status int, settings []alerts.Setting, channels *alerts.Channels, msg string) {
	v := alertSettingsView{Settings: settings, Frequencies: alerts.Frequencies, Error: msg}
	var err error
	if v.Settings == nil {
		v.Settings, err = s.alerts.Settings(r.Context())
	}
	if err == nil && channels == nil {
		v.Channels, err = s.alerts.Channels(r.Context())
	} else if channels != nil {
		v.Channels = *channels
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	v.Channels.SMTPPassword = "" // jamais réaffiché
	s.render(w, r, status, "alert_settings", "Réglages des alertes", v)
}

func (s *Server) saveAlertSettings(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	settings := make([]alerts.Setting, len(alerts.Kinds))
	for i, k := range alerts.Kinds {
		param, _ := strconv.Atoi(r.PostFormValue("param_" + string(k)))
		settings[i] = alerts.Setting{Kind: k, Enabled: r.PostFormValue("enabled_"+string(k)) != "", Param: param}
	}
	if err := s.alerts.SaveSettings(r.Context(), settings); err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.renderAlertSettings(w, r, http.StatusUnprocessableEntity, settings, nil, msg)
		}
		return
	}
	s.redirect(w, r, "/alertes/reglages", "Réglages des alertes enregistrés.")
}

func (s *Server) saveChannels(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	port, _ := strconv.Atoi(r.PostFormValue("smtp_port"))
	c := alerts.Channels{
		EmailEnabled: r.PostFormValue("email_enabled") != "", SMTPHost: r.PostFormValue("smtp_host"), SMTPPort: port,
		SMTPSecurity: r.PostFormValue("smtp_security"), SMTPUser: r.PostFormValue("smtp_user"), SMTPPassword: r.PostFormValue("smtp_password"),
		EmailFrom: r.PostFormValue("email_from"), EmailTo: r.PostFormValue("email_to"),
		WebhookEnabled: r.PostFormValue("webhook_enabled") != "", WebhookURL: r.PostFormValue("webhook_url"),
		Frequency: alerts.Frequency(r.PostFormValue("frequency")), Discreet: r.PostFormValue("discreet") != "",
	}
	if err := s.alerts.SaveChannels(r.Context(), c); err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			s.renderAlertSettings(w, r, http.StatusUnprocessableEntity, nil, &c, msg)
		}
		return
	}
	s.redirect(w, r, "/alertes/reglages", "Canaux de notification enregistrés.")
}

func (s *Server) testChannels(w http.ResponseWriter, r *http.Request) {
	if err := s.alerts.SendTest(r.Context()); err != nil {
		s.failed(w, r, err, "/alertes/reglages")
		return
	}
	s.redirect(w, r, "/alertes/reglages", "Notification d'essai envoyée.")
}
