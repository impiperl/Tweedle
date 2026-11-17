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
		"endpoint_down":       "{what} nicht erreichbar: {error}",
		"endpoint_slow":       "{what} antwortet langsam ({value} ms, Grenze {limit} ms)",
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
		"lbl_endpoint":  "Erreichbarkeit {what}",
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
		// install
		"inst_need_root":         "Bitte mit sudo starten: sudo tweedle install",
		"inst_need_systemd":      "tweedle install braucht systemd. Ohne systemd: tweedle serve selbst starten.",
		"inst_intro":             "Tweedle einrichten. Enter übernimmt den Vorschlag in [Klammern]. Config: {path}",
		"inst_yes_no_y":          "J/n",
		"inst_yes_no_n":          "j/N",
		"inst_name":              "Name des Servers (erscheint in der App und in Push-Nachrichten)",
		"inst_mode_q":            "Wie erreicht die App diesen Server?",
		"inst_mode_proxy":        "Über einen Webserver, der hier schon läuft (nginx, Apache, Caddy …) – empfohlen",
		"inst_mode_tls":          "Direkt über HTTPS mit eigenem Zertifikat (z. B. von Let's Encrypt)",
		"inst_mode_plain":        "Direkt ohne Verschlüsselung – nur im Heimnetz oder über VPN",
		"inst_choice":            "Auswahl",
		"inst_port":              "Port für Tweedle",
		"inst_port_invalid":      "Bitte eine Zahl von 1 bis 65535.",
		"inst_podman_q":          "Läuft der Webserver in einem Podman-Container? Dann dessen Netzwerk (leer oder - = nein)",
		"inst_podman_ok":         "Tweedle lauscht im Netzwerk {network} auf {gateway}; der Container erreicht es als host.containers.internal.",
		"inst_podman_failed":     "Netzwerk nicht gefunden: {error}",
		"inst_public_url":        "Adresse, unter der der Webserver Tweedle weiterleitet",
		"inst_url_invalid":       "Bitte eine Adresse mit http:// oder https://.",
		"inst_domain":            "Domain dieses Servers",
		"inst_cert":              "Zertifikat (PEM, z. B. fullchain.pem)",
		"inst_key":               "Privater Schlüssel (PEM, z. B. privkey.pem)",
		"inst_cert_invalid":      "Zertifikat oder Schlüssel nicht lesbar: {error}",
		"inst_address":           "IP-Adresse oder Name, unter dem die App den Server erreicht",
		"inst_plain_warning":     "Achtung: ohne Verschlüsselung sind Daten und Zugangsschlüssel mitlesbar. Nur im Heimnetz oder VPN verwenden.",
		"inst_watching":          "Überwacht schon: {name} ({url}, {state})",
		"inst_on":                "an",
		"inst_off":               "aus",
		"inst_other_q":           "Anderen Tweedle-Server mitüberwachen – Adresse (leer = keiner)",
		"inst_other_ok":          "Erreichbar: {name}",
		"inst_other_unreachable": "Nicht erreichbar: {error}",
		"inst_other_anyway":      "Trotzdem eintragen?",
		"inst_cert_watch_q":      "Ablauf des Zertifikats von {host} überwachen?",
		"inst_select_q":          "Prozesse und Container jetzt auswählen?",
		"inst_saved":             "Config gespeichert: {path}",
		"inst_service_ok":        "Dienst tweedle eingerichtet und gestartet.",
		"inst_health_ok":         "Tweedle antwortet: {url}",
		"inst_health_failed":     "Tweedle antwortet nicht auf {url}. Log: journalctl -u tweedle -n 30",
		"inst_proxy_intro":       "Jetzt im Webserver {prefix} an Tweedle weiterleiten (im Server-Block für {host}), dann den Webserver neu laden:",
		"inst_proxy_check":       "Danach prüfen: curl {health}",
		"inst_firewall":          "Port {port}/tcp in der Firewall freigeben, z. B. firewall-cmd --permanent --add-port={port}/tcp && firewall-cmd --reload oder ufw allow {port}/tcp",
		"inst_done":              "Fertig. Die App erreicht den Server unter {url}. Koppeln: sudo tweedle pair",
		// status
		"status_processes":  "Prozesse",
		"status_containers": "Container",
		"status_certs":      "Zertifikate",
		"status_endpoints":  "Erreichbarkeit",
		"status_days":       "noch {days} Tage",
		"status_expired":    "abgelaufen",
		"status_none":       "(keine ausgewählt)",
		// pair, devices
		"pair_no_url":       "public_url fehlt in der Config: sudo tweedle install ausführen (oder --url angeben, z. B. https://example.com/tweedle)",
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
		"endpoint_down":       "{what} is unreachable: {error}",
		"endpoint_slow":       "{what} is slow ({value} ms, limit {limit} ms)",
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
		"lbl_endpoint":        "Reachability {what}",
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
		// install
		"inst_need_root":         "Please run with sudo: sudo tweedle install",
		"inst_need_systemd":      "tweedle install needs systemd. Without systemd, start tweedle serve yourself.",
		"inst_intro":             "Setting up Tweedle. Enter takes the suggestion in [brackets]. Config: {path}",
		"inst_yes_no_y":          "Y/n",
		"inst_yes_no_n":          "y/N",
		"inst_name":              "Name of this server (shown in the app and in push messages)",
		"inst_mode_q":            "How does the app reach this server?",
		"inst_mode_proxy":        "Through a web server already running here (nginx, Apache, Caddy …) – recommended",
		"inst_mode_tls":          "Directly over HTTPS with its own certificate (e.g. from Let's Encrypt)",
		"inst_mode_plain":        "Directly without encryption – only on a home network or VPN",
		"inst_choice":            "Choice",
		"inst_port":              "Port for Tweedle",
		"inst_port_invalid":      "Please enter a number from 1 to 65535.",
		"inst_podman_q":          "Does the web server run in a Podman container? Then its network (empty or - = no)",
		"inst_podman_ok":         "Tweedle listens in network {network} on {gateway}; the container reaches it as host.containers.internal.",
		"inst_podman_failed":     "Network not found: {error}",
		"inst_public_url":        "Address under which the web server forwards to Tweedle",
		"inst_url_invalid":       "Please enter an address starting with http:// or https://.",
		"inst_domain":            "Domain of this server",
		"inst_cert":              "Certificate (PEM, e.g. fullchain.pem)",
		"inst_key":               "Private key (PEM, e.g. privkey.pem)",
		"inst_cert_invalid":      "Cannot read certificate or key: {error}",
		"inst_address":           "IP address or name under which the app reaches the server",
		"inst_plain_warning":     "Warning: without encryption, data and access keys can be read on the way. Use only on a home network or VPN.",
		"inst_watching":          "Already watching: {name} ({url}, {state})",
		"inst_on":                "on",
		"inst_off":               "off",
		"inst_other_q":           "Also watch another Tweedle server – address (empty = none)",
		"inst_other_ok":          "Reachable: {name}",
		"inst_other_unreachable": "Not reachable: {error}",
		"inst_other_anyway":      "Add it anyway?",
		"inst_cert_watch_q":      "Watch the certificate expiry of {host}?",
		"inst_select_q":          "Choose processes and containers now?",
		"inst_saved":             "Config saved: {path}",
		"inst_service_ok":        "Service tweedle set up and started.",
		"inst_health_ok":         "Tweedle answers: {url}",
		"inst_health_failed":     "Tweedle does not answer on {url}. Log: journalctl -u tweedle -n 30",
		"inst_proxy_intro":       "Now forward {prefix} to Tweedle in the web server (server block for {host}), then reload the web server:",
		"inst_proxy_check":       "Then check: curl {health}",
		"inst_firewall":          "Open port {port}/tcp in the firewall, e.g. firewall-cmd --permanent --add-port={port}/tcp && firewall-cmd --reload or ufw allow {port}/tcp",
		"inst_done":              "Done. The app reaches the server at {url}. Pair: sudo tweedle pair",
		// status
		"status_processes":  "Processes",
		"status_containers": "Containers",
		"status_certs":      "Certificates",
		"status_endpoints":  "Reachability",
		"status_days":       "{days} days left",
		"status_expired":    "expired",
		"status_none":       "(none selected)",
		"pair_no_url":       "public_url is missing in the config: run sudo tweedle install (or pass --url, e.g. https://example.com/tweedle)",
		"pair_info":         "Server {name} · {url}\nCode to type in: {code} · valid until {time}, single use",
		"devices_none":      "No devices paired.",
		"devices_removed":   "Device {id} removed.",
		"devices_not_found": "Device {id} not found.",
		"push_title":        "{server} · {level}",
		"push_summary":      "{n} messages, including: {first}",
		"push_test":         "Test message from Tweedle – push works.",
		"push_test_result":  "{sent} sent, {failed} failed, {skipped} devices without push token.",
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
