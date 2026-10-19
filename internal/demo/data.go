package demo

import (
	"encoding/base64"
	"fmt"
	"html"
	"strings"
)

// tinyPNG is a valid 2x2 PNG used for inline images and attachments.
var tinyPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFklEQVR42mNk+M9Qz0AEYBxVSF+FAP5FDvcfRYWgAAAAAElFTkSuQmCC")

func fakePDF(title string) []byte {
	return []byte("%PDF-1.4\n% gomail demo document\n1 0 obj << /Title (" + title + ") >> endobj\ntrailer << /Root 1 0 R >>\n%%EOF\n")
}

type tmplAttachment struct {
	name, mime string
	data       func() []byte
}

type template struct {
	fromName, fromEmail string
	subject             string
	paragraphs          []string
	newsletter          bool // HTML layout with header image
	labels              []string
	attachments         []tmplAttachment
	folder              string // "" = inbox
	starred             bool
	replies             []string // follow-up messages in the same thread (alternating me/them)
	rawHTML             string   // complete body (used for sent mail)
}

var templates = []template{
	{fromName: "Priya Raman", fromEmail: "priya.raman@northwind.io", subject: "Q4 roadmap review — slides attached",
		paragraphs: []string{"Hi,", "Attached are the slides for Thursday's roadmap review. The big open question is whether we ship offline sync in Q4 or push it to January.", "Could you take a look at slides 6–9 before the meeting and add your comments?", "Thanks!<br>Priya"},
		labels:     []string{"Work"}, attachments: []tmplAttachment{{"Q4-roadmap.pdf", "application/pdf", func() []byte { return fakePDF("Q4 roadmap") }}}, starred: true,
		replies: []string{"Thanks Priya, I'll go through them tonight. My vote is January — we need another round of testing.", "Makes sense. Let's discuss on Thursday then."}},
	{fromName: "Acme Cloud Billing", fromEmail: "billing@acmecloud.com", subject: "Your invoice for September is ready",
		paragraphs: []string{"Hello,", "Your invoice <b>INV-2026-0931</b> for <b>$248.17</b> is now available. The amount will be charged to your card ending in 4242 on October 15.", "You can download the PDF from the attachment or from your billing dashboard."},
		labels:     []string{"Finance"}, attachments: []tmplAttachment{{"INV-2026-0931.pdf", "application/pdf", func() []byte { return fakePDF("Invoice") }}}},
	{fromName: "Mom", fromEmail: "linda.visser@example.nl", subject: "Sunday dinner?",
		paragraphs: []string{"Hi sweetheart,", "Are you coming for dinner on Sunday? Dad is making his famous lasagna. Let me know if you're bringing anyone!", "Love, Mom"},
		labels:     []string{"Family"}, replies: []string{"Wouldn't miss it! I'll bring dessert 🍰", "Perfect, see you at 6!"}},
	{fromName: "GitHub", fromEmail: "noreply@github.com", subject: "[gomail] CI failed on main: go test ./...",
		paragraphs: []string{"<b>Run failed</b> for commit <code>a1b2c3d</code> on branch <code>main</code>.", "<code>--- FAIL: TestSearchTypo (0.02s)</code>", "View the run on GitHub to see the full logs."}},
	{fromName: "The Morning Brew", fromEmail: "crew@morningbrew.example", subject: "☕ Markets rally, AI chips, and a very good dog",
		paragraphs: []string{"Good morning. Stocks closed at record highs yesterday as chipmakers rallied on strong earnings.", "Also in today's issue: why everyone is suddenly talking about decision models, and the dog who became mayor of a small town in Vermont.", "Read time: 4 minutes."},
		newsletter: true},
	{fromName: "KLM Royal Dutch Airlines", fromEmail: "noreply@klm.example", subject: "Your booking confirmation AMS → LIS, 14 Nov",
		paragraphs: []string{"Dear traveller,", "Thank you for booking with us. Your booking code is <b>X7K2PQ</b>.", "Flight KL1693 departs Amsterdam (AMS) on 14 November at 07:10 and arrives in Lisbon (LIS) at 09:05.", "Online check-in opens 30 hours before departure."},
		labels:     []string{"Travel"}, attachments: []tmplAttachment{{"e-ticket-X7K2PQ.pdf", "application/pdf", func() []byte { return fakePDF("E-ticket") }}}, starred: true},
	{fromName: "LinkedIn", fromEmail: "notifications@linkedin.example", subject: "You appeared in 14 searches this week",
		paragraphs: []string{"Your profile is getting noticed.", "See who's looking and grow your network."}, newsletter: true},
	{fromName: "Daniel Okafor", fromEmail: "daniel@okafor.dev", subject: "Code review: new sync engine",
		paragraphs: []string{"Hey,", "I left a few comments on the PR. Mostly small things, but I think the retry logic in the Graph client should honor Retry-After on batched sub-requests too.", "Otherwise LGTM 👍"},
		labels:     []string{"Work"}, replies: []string{"Good catch, pushed a fix.", "Approved. Ship it!"}},
	{fromName: "Bol.com", fromEmail: "service@bol.example", subject: "Je bestelling is onderweg 📦",
		paragraphs: []string{"Goed nieuws! Je bestelling <b>#4417-2290</b> is vandaag verzonden.", "Verwachte bezorging: morgen tussen 14:00 en 17:00."}},
	{fromName: "Spotify", fromEmail: "no-reply@spotify.example", subject: "Your Release Radar is here",
		paragraphs: []string{"New music from artists you follow, updated every Friday.", "Listen now."}, newsletter: true},
	{fromName: "Sofia Martins", fromEmail: "sofia.martins@example.pt", subject: "Lisbon trip — restaurant list",
		paragraphs: []string{"Olá!", "Since you're coming in November, here's my list of places you can't miss: Taberna da Rua das Flores, Cervejaria Ramiro, and the pastéis at Manteigaria.", "Let me know when you land and we'll grab coffee ☕"},
		labels:     []string{"Travel"}, replies: []string{"Obrigado Sofia! Saving all of these. Coffee on the 15th?", "Deal! 15th it is."}},
	{fromName: "Stripe", fromEmail: "receipts@stripe.example", subject: "Receipt from Figma — $15.00",
		paragraphs: []string{"Receipt #1934-7731", "Figma Professional — monthly", "Amount paid: <b>$15.00</b>"}, labels: []string{"Finance"}},
	{fromName: "Google Security", fromEmail: "no-reply@accounts.google.example", subject: "Security alert: new sign-in on Linux",
		paragraphs: []string{"We noticed a new sign-in to your account on a Linux device.", "If this was you, you don't need to do anything. If not, we'll help you secure your account."}, starred: true},
	{fromName: "Jonas Becker", fromEmail: "jonas.becker@example.de", subject: "Grüße aus München & Oktoberfest-Fotos",
		paragraphs: []string{"Servus!", "Anbei ein paar Fotos vom Oktoberfest. Das Wetter war überraschend gut.", "Bis bald,<br>Jonas"},
		labels:     []string{"Family"}, attachments: []tmplAttachment{{"wiesn-2026.png", "image/png", func() []byte { return tinyPNG }}}},
	{fromName: "Figma", fromEmail: "team@figma.example", subject: "Introducing Dev Mode 3.0",
		paragraphs: []string{"Faster handoff, smarter code snippets and a new inspect panel.", "Try it today in any file."}, newsletter: true},
	{fromName: "Calendar", fromEmail: "calendar-notification@example.com", subject: "Invitation: Design sync @ Wed 10:00 – 10:30",
		paragraphs: []string{"You have been invited to <b>Design sync</b>.", "When: Wednesday 10:00 – 10:30 (CET)<br>Where: Meet room 4"},
		labels:     []string{"Work"}, attachments: []tmplAttachment{{"invite.ics", "text/calendar", func() []byte {
			return []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nSUMMARY:Design sync\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
		}}}},
	{fromName: "Mega Prize Center", fromEmail: "winner@prizes-4u.example", subject: "CONGRATULATIONS!!! You have WON an iPhone 17",
		paragraphs: []string{"Click here to claim your prize before it expires!!!", "Only today. Limited offer."}, folder: "spam"},
	{fromName: "Crypto Insider", fromEmail: "alpha@crypto-insider.example", subject: "This coin will 100x next week",
		paragraphs: []string{"Don't miss out on the next big thing."}, folder: "spam"},
	{fromName: "Hacker News Digest", fromEmail: "digest@hndigest.example", subject: "Top stories: SQLite is all you need, Go 1.26 released",
		paragraphs: []string{"1. SQLite is all you need (2,140 points)", "2. Go 1.26 released (1,233 points)", "3. Show HN: A desktop mail client built with Wails (412 points)"}, newsletter: true},
	{fromName: "Landlord — Peter de Vries", fromEmail: "peter.devries@example.nl", subject: "Maintenance visit next Tuesday",
		paragraphs: []string{"Hello,", "The plumber will come by next Tuesday between 9 and 12 to check the boiler. Please make sure someone is home.", "Kind regards,<br>Peter"}},
	{fromName: "ING", fromEmail: "info@ing.example", subject: "Your monthly statement is available",
		paragraphs: []string{"Your statement for September is ready in the app.", "Balance at end of period: <b>€3,412.55</b>"}, labels: []string{"Finance"}},
	{fromName: "Meetup", fromEmail: "info@meetup.example", subject: "Amsterdam Go Meetup: Building desktop apps in Go",
		paragraphs: []string{"Join us on November 3rd for talks about Wails, Fyne and Gio.", "Pizza and drinks provided 🍕"}},
	{fromName: "Notion", fromEmail: "notify@notion.example", subject: "Daniel commented on “Sync engine design”",
		paragraphs: []string{"“Should we batch label updates through $batch as well?”", "Reply in Notion."}, labels: []string{"Work"}},
	{fromName: "Booking.com", fromEmail: "noreply@booking.example", subject: "Your stay at Casa do Bairro is confirmed",
		paragraphs: []string{"Check-in: Thu 14 Nov from 15:00<br>Check-out: Sun 17 Nov until 11:00", "Confirmation number: 4021.773.118"}, labels: []string{"Travel"}},
}

var sentTemplates = []template{
	{fromName: "", subject: "Re: Contract renewal", paragraphs: []string{"Hi Mark,", "Looks good to me — I've signed and attached the contract.", "Best"},
		attachments: []tmplAttachment{{"contract-signed.pdf", "application/pdf", func() []byte { return fakePDF("Contract") }}}, labels: []string{"Work"}},
	{fromName: "", subject: "Photos from the weekend", paragraphs: []string{"Here you go! Some great ones of the kids at the beach."}, labels: []string{"Family"}},
	{fromName: "", subject: "Expense report October", paragraphs: []string{"Hi finance team,", "Please find my October expense report attached."},
		attachments: []tmplAttachment{{"expenses-oct.csv", "text/csv", func() []byte { return []byte("date,item,amount\n2026-10-02,Train,23.40\n2026-10-09,Lunch,18.00\n") }}}, labels: []string{"Finance"}},
}

var draftTemplates = []template{
	{subject: "Ideas for the team offsite", paragraphs: []string{"Some ideas:", "- Hiking day in the dunes<br>- Cooking workshop<br>- Escape room"}},
}

func htmlBody(t template, cid string) string {
	var b strings.Builder
	if t.newsletter {
		b.WriteString(`<table width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f7;padding:24px 0"><tr><td align="center">`)
		b.WriteString(`<table width="600" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:8px;font-family:Helvetica,Arial,sans-serif;color:#1f2937">`)
		if cid != "" {
			b.WriteString(`<tr><td style="padding:24px 24px 0"><img src="cid:` + cid + `" width="48" height="48" alt="logo"></td></tr>`)
		}
		b.WriteString(`<tr><td style="padding:16px 24px 0;font-size:22px;font-weight:bold">` + html.EscapeString(t.subject) + `</td></tr>`)
		for _, p := range t.paragraphs {
			b.WriteString(`<tr><td style="padding:12px 24px;font-size:15px;line-height:1.5">` + p + `</td></tr>`)
		}
		b.WriteString(`<tr><td style="padding:16px 24px 24px"><a href="https://example.com/read" style="background:#2563eb;color:#fff;padding:10px 18px;border-radius:6px;text-decoration:none">Read more</a></td></tr>`)
		b.WriteString(`<tr><td style="padding:12px 24px;font-size:12px;color:#6b7280">You are receiving this because you subscribed. <a href="https://example.com/unsubscribe">Unsubscribe</a></td></tr>`)
		b.WriteString(`</table></td></tr></table>`)
		return b.String()
	}
	b.WriteString(`<div style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;font-size:14px;line-height:1.5">`)
	for _, p := range t.paragraphs {
		b.WriteString("<p>" + p + "</p>")
	}
	b.WriteString("</div>")
	return b.String()
}

func replyHTML(text string) string {
	return fmt.Sprintf(`<div style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;font-size:14px"><p>%s</p></div>`, html.EscapeString(text))
}
