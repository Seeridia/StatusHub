package notify

import (
	"bytes"
	"html/template"
	"net/url"
	"strings"
	"time"
)

type emailMessage struct {
	Subject string
	Text    string
	HTML    string
}

var notificationEmail = template.Must(template.New("notification").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body style="margin:0;background:#f3f5f7;font-family:Arial,Helvetica,sans-serif;color:#20252b">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center" style="padding:32px 16px">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" style="width:100%;max-width:600px">
<tr><td align="center" style="padding:0 0 24px"><table role="presentation" align="center" cellpadding="0" cellspacing="0"><tr><td style="padding-right:10px;vertical-align:middle"><img src="cid:statushub-logo" width="38" height="32" alt="" style="display:block;border:0"></td><td style="vertical-align:middle;font-size:24px;font-weight:bold">Status<span style="color:#168565">Hub</span></td></tr></table></td></tr>
<tr><td style="background:#ffffff;border:1px solid #e1e5e9;border-radius:12px;padding:32px">
<p style="margin:0 0 12px;color:#637080;font-size:12px;letter-spacing:1px">SERVICE STATUS UPDATE</p>
<h1 style="margin:0 0 24px;font-size:26px;line-height:1.3;overflow-wrap:anywhere">{{.Title}}</h1>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:14px;line-height:1.6">{{range .Fields}}<tr><td width="100" valign="top" style="padding:8px 12px 8px 0;color:#637080;border-bottom:1px solid #edf0f2">{{.Label}}</td><td style="padding:8px 0;border-bottom:1px solid #edf0f2;overflow-wrap:anywhere">{{.Value}}</td></tr>{{end}}</table>
{{if .Body}}<h2 style="font-size:16px;margin:28px 0 12px">Latest update</h2><p style="font-size:15px;line-height:1.7;white-space:pre-wrap;overflow-wrap:anywhere;margin:0">{{.Body}}</p>{{end}}
{{if .URL}}<table role="presentation" cellpadding="0" cellspacing="0" style="margin-top:28px"><tr><td bgcolor="#087b5c" style="border-radius:6px"><a href="{{.URL}}" style="display:inline-block;padding:12px 20px;color:#ffffff;text-decoration:none;font-size:14px;font-weight:bold">View original update →</a></td></tr></table>{{end}}
</td></tr><tr><td style="padding:20px 4px;color:#697586;font-size:12px;line-height:1.6">Sent by a StatusHub notification rule. Manage recipients and delivery preferences in Notification channels.<br>All timestamps are UTC.</td></tr></table>
</td></tr></table></body></html>`))

func renderEmail(event CanonicalEvent) (emailMessage, error) {
	d := parseEventDetails(event.Data)
	title := strings.TrimSpace(event.Subject)
	if title == "" {
		title = d.Current.Name
	}
	if title == "" {
		title = string(event.Kind)
	}
	title, _ = truncateRunes(title, 180)
	type field struct{ Label, Value string }
	view := struct {
		Title, Body, URL string
		Fields           []field
	}{Title: title}
	add := func(label, value string) {
		if value = strings.TrimSpace(value); value != "" {
			value, _ = truncateRunes(value, 250)
			view.Fields = append(view.Fields, field{label, value})
		}
	}
	add("Event", string(event.Kind))
	add("Status", statusChange(d.Previous.Status, d.Current.Status))
	add("Phase", statusChange(d.Previous.Phase, d.Current.Phase))
	add("Impact", d.Current.Impact)
	if !event.Time.IsZero() {
		add("Updated", event.Time.UTC().Format(time.RFC3339))
	}
	view.Body = latestBody(d.Current.Updates)
	if view.Body == "" {
		view.Body = d.Current.Description
	}
	if view.Body == "" && d.Current.Status == "" && d.Current.Phase == "" {
		view.Body = event.Summary
	}
	view.Body, _ = truncateRunes(view.Body, 1800)
	if link, err := url.Parse(d.Current.URL); err == nil && link.Host != "" && link.User == nil && (link.Scheme == "https" || link.Scheme == "http") && len(link.String()) <= 2048 {
		view.URL = link.String()
	}
	var html bytes.Buffer
	if err := notificationEmail.Execute(&html, view); err != nil {
		return emailMessage{}, err
	}
	text := "StatusHub — " + title + "\n"
	for _, f := range view.Fields {
		text += f.Label + ": " + f.Value + "\n"
	}
	text += "\n" + view.Body
	if view.URL != "" {
		text += "\n\n" + view.URL
	}
	return emailMessage{Subject: "[StatusHub] " + title, Text: text, HTML: html.String()}, nil
}
