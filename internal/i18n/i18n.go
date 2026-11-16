// Package i18n holds the texts Tweedle produces itself (terminal, push
// messages) in German and English. Events carry a code plus parameters and
// are rendered per receiver language.
package i18n

import (
	"sort"
	"strings"
)

var texts = map[string]map[string]string{
	"de": {
		// events
		"started":             "Tweedle auf {server} gestartet",
		"recovered":           "{label}: wieder in Ordnung",
		"flapping":            "{label} wechselt ständig den Zustand, Meldungen pausiert",
		"flapping_end":        "{label} ist wieder stabil",
		"cpu_high":            "CPU bei {value} % (Grenze {limit} %)",
		"mem_high":            "RAM bei {value} % (Grenze {limit} %)",
		"swap_high":           "Swap bei {value} % (Grenze {limit} %)",
		"disk_high":           "Platte {mount} zu {value} % voll (Grenze {limit} %)",
		"inodes_high":         "Inodes auf {mount} zu {value} % belegt (Grenze {limit} %)",
		"load_high":           "Load {value} bei {cores} Kernen (Grenze {limit})",
		"proc_down":           "Prozess {what} läuft nicht ({count} von mindestens {min})",
		"proc_restarted":      "Prozess {what} wurde neu gestartet",
		"container_missing":   "Container {what} nicht gefunden",
		"container_down":      "Container {what} läuft nicht (Status: {state})",
		"container_unhealthy": "Container {what} ist unhealthy",
		"container_restarted": "Container {what} wurde neu gestartet",
		"podman_failed":       "podman-Abfrage fehlgeschlagen: {error}",
		"cert_expiring":       "Zertifikat {what} läuft in {days} Tagen ab ({date})",
		"cert_expired":        "Zertifikat {what} ist seit {date} abgelaufen",
		"cert_invalid":        "Zertifikat {what} ist ungültig: {error}",
		"cert_failed":         "Zertifikat {what} nicht prüfbar: {error}",
		// labels (what a check is about)
		"lbl_cpu":       "CPU",
		"lbl_mem":       "RAM",
		"lbl_swap":      "Swap",
		"lbl_disk":      "Platte {mount}",
		"lbl_inodes":    "Inodes {mount}",
		"lbl_load":      "Load",
		"lbl_proc":      "Prozess {what}",
		"lbl_container": "Container {what}",
		"lbl_podman":    "podman",
		"lbl_cert":      "Zertifikat {what}",
		// levels
		"level_info":  "INFO",
		"level_warn":  "WARN",
		"level_error": "FEHLER",
		"level_ok":    "OK",
		// setup
		"setup_title":       "Tweedle – was soll überwacht werden?",
		"setup_help":        "↑/↓ bewegen · Leertaste an/aus · / suchen · Enter speichern · q abbrechen",
		"setup_filter":      "Suche: ",
		"setup_containers":  "Container",
		"setup_processes":   "Prozesse",
		"setup_not_running": "(läuft gerade nicht)",
		"setup_selected":    "{n} ausgewählt",
		"setup_saved":       "Gespeichert: {path}",
		"setup_aborted":     "Abgebrochen, nichts gespeichert.",
		"setup_podman_err":  "Container konnten nicht gelesen werden: {error}",
		"setup_restart":     "Läuft der Dienst schon, danach: sudo systemctl restart tweedle",
		// status
		"status_processes":  "Prozesse",
		"status_containers": "Container",
		"status_certs":      "Zertifikate",
		"status_days":       "noch {days} Tage",
		"status_expired":    "abgelaufen",
		"status_none":       "(keine ausgewählt)",
		// pair, devices
		"pair_no_url":       "public_url fehlt in der Config (oder --url angeben), z. B. https://gaistnet.com/mon",
		"pair_info":         "Server {name} · {url}\nCode zum Abtippen: {code} · gültig bis {time}, nur einmal verwendbar",
		"devices_none":      "Keine Geräte gekoppelt.",
		"devices_removed":   "Gerät {id} entfernt.",
		"devices_not_found": "Gerät {id} nicht gefunden.",
		// push
		"push_title":       "{server} · {level}",
		"push_summary":     "{n} Meldungen, u. a.: {first}",
		"push_test":        "Testnachricht von Tweedle – Push funktioniert.",
		"push_test_result": "{sent} gesendet, {failed} fehlgeschlagen, {skipped} Geräte ohne Push-Token.",
	},
	"en": {
		"started":             "Tweedle started on {server}",
		"recovered":           "{label}: back to normal",
		"flapping":            "{label} keeps changing state, messages paused",
		"flapping_end":        "{label} is stable again",
		"cpu_high":            "CPU at {value} % (limit {limit} %)",
		"mem_high":            "RAM at {value} % (limit {limit} %)",
		"swap_high":           "Swap at {value} % (limit {limit} %)",
		"disk_high":           "Disk {mount} {value} % full (limit {limit} %)",
		"inodes_high":         "Inodes on {mount} {value} % used (limit {limit} %)",
		"load_high":           "Load {value} on {cores} cores (limit {limit})",
		"proc_down":           "Process {what} is not running ({count} of at least {min})",
		"proc_restarted":      "Process {what} was restarted",
		"container_missing":   "Container {what} not found",
		"container_down":      "Container {what} is not running (state: {state})",
		"container_unhealthy": "Container {what} is unhealthy",
		"container_restarted": "Container {what} was restarted",
		"podman_failed":       "podman query failed: {error}",
		"cert_expiring":       "Certificate {what} expires in {days} days ({date})",
		"cert_expired":        "Certificate {what} expired on {date}",
		"cert_invalid":        "Certificate {what} is invalid: {error}",
		"cert_failed":         "Certificate {what} could not be checked: {error}",
		"lbl_cpu":             "CPU",
		"lbl_mem":             "RAM",
		"lbl_swap":            "Swap",
		"lbl_disk":            "Disk {mount}",
		"lbl_inodes":          "Inodes {mount}",
		"lbl_load":            "Load",
		"lbl_proc":            "Process {what}",
		"lbl_container":       "Container {what}",
		"lbl_podman":          "podman",
		"lbl_cert":            "Certificate {what}",
		"level_info":          "INFO",
		"level_warn":          "WARN",
		"level_error":         "ERROR",
		"level_ok":            "OK",
		"setup_title":         "Tweedle – what should be watched?",
		"setup_help":          "↑/↓ move · space toggle · / search · enter save · q cancel",
		"setup_filter":        "Search: ",
		"setup_containers":    "Containers",
		"setup_processes":     "Processes",
		"setup_not_running":   "(not running right now)",
		"setup_selected":      "{n} selected",
		"setup_saved":         "Saved: {path}",
		"setup_aborted":       "Cancelled, nothing saved.",
		"setup_podman_err":    "Could not read containers: {error}",
		"setup_restart":       "If the service is already running: sudo systemctl restart tweedle",
		"status_processes":    "Processes",
		"status_containers":   "Containers",
		"status_certs":        "Certificates",
		"status_days":         "{days} days left",
		"status_expired":      "expired",
		"status_none":         "(none selected)",
		"pair_no_url":         "public_url is missing in the config (or pass --url), e.g. https://gaistnet.com/mon",
		"pair_info":           "Server {name} · {url}\nCode to type in: {code} · valid until {time}, single use",
		"devices_none":        "No devices paired.",
		"devices_removed":     "Device {id} removed.",
		"devices_not_found":   "Device {id} not found.",
		"push_title":          "{server} · {level}",
		"push_summary":        "{n} messages, including: {first}",
		"push_test":           "Test message from Tweedle – push works.",
		"push_test_result":    "{sent} sent, {failed} failed, {skipped} devices without push token.",
	},
}

// Langs returns the supported language codes.
func Langs() []string {
	out := make([]string, 0, len(texts))
	for l := range texts {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Supported reports whether lang is a known language.
func Supported(lang string) bool { _, ok := texts[lang]; return ok }

// T renders code in lang, replacing {name} with params. If params contains
// "label", that value is itself a code and is rendered first. Unknown
// languages fall back to English, unknown codes to the code itself.
func T(lang, code string, params map[string]string) string {
	tpl, ok := lookup(lang, code)
	if !ok {
		return code
	}
	if lbl, ok := params["label"]; ok {
		rendered := T(lang, lbl, withoutLabel(params))
		tpl = strings.ReplaceAll(tpl, "{label}", rendered)
	}
	for k, v := range params {
		tpl = strings.ReplaceAll(tpl, "{"+k+"}", v)
	}
	return tpl
}

func lookup(lang, code string) (string, bool) {
	if m, ok := texts[lang]; ok {
		if s, ok := m[code]; ok {
			return s, true
		}
	}
	s, ok := texts["en"][code]
	return s, ok
}

func withoutLabel(p map[string]string) map[string]string {
	out := make(map[string]string, len(p))
	for k, v := range p {
		if k != "label" {
			out[k] = v
		}
	}
	return out
}
