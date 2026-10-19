// Package demo provides in-memory fake mail accounts (one Gmail-like, one
// Outlook-like) with realistic messages, so gomail can be explored without
// OAuth credentials (GOMAIL_DEMO=1). It is also used by tests.
package demo

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/mailmime"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
	"github.com/AVMG20/gomail/internal/provider/gmail"
	"github.com/AVMG20/gomail/internal/provider/outlook"
)

// Demo account ids.
const (
	GmailAccountID   = "demo-gmail"
	OutlookAccountID = "demo-outlook"
)

// Accounts returns the demo accounts.
func Accounts() []models.Account {
	return []models.Account{
		{ID: GmailAccountID, Provider: models.ProviderGmail, Email: "alex.demo@gmail.com", DisplayName: "Alex (Gmail)", Color: "#ea4335"},
		{ID: OutlookAccountID, Provider: models.ProviderOutlook, Email: "alex.demo@outlook.com", DisplayName: "Alex (Outlook)", Color: "#0078d4"},
	}
}

type message struct {
	meta mail.Message
	raw  []byte
}

// Provider is an in-memory provider.Provider.
type Provider struct {
	mu       sync.Mutex
	kind     string
	me       models.Address
	instance string
	folders  []mail.Folder
	labels   []mail.Label
	msgs     map[string]*message
	version  int
	changes  []change
	rng      *rand.Rand
	seq      int
	// NewMailChance is the probability that an incremental sync delivers a
	// new message (simulated incoming mail).
	NewMailChance float64
	// Latency simulates network delay per call.
	Latency time.Duration
}

type change struct {
	version int
	id      string
	deleted bool
}

// New creates a demo provider for an account from Accounts().
func New(acct models.Account) *Provider {
	seed := uint64(1)
	if acct.Provider == models.ProviderOutlook {
		seed = 2
	}
	p := &Provider{
		kind:          acct.Provider,
		me:            models.Address{Name: "Alex Demo", Email: acct.Email},
		instance:      strconv.FormatInt(time.Now().UnixNano(), 36),
		msgs:          map[string]*message{},
		rng:           rand.New(rand.NewPCG(seed, 42)),
		NewMailChance: 0.35,
		Latency:       150 * time.Millisecond,
	}
	p.setupFolders()
	p.seed(time.Now())
	return p
}

func (p *Provider) isGmail() bool { return p.kind == models.ProviderGmail }

func (p *Provider) setupFolders() {
	if p.isGmail() {
		p.folders = []mail.Folder{
			{RemoteID: "INBOX", Name: "Inbox", Kind: models.FolderInbox}, {RemoteID: "STARRED", Name: "Starred", Kind: models.FolderStarred},
			{RemoteID: "SENT", Name: "Sent", Kind: models.FolderSent}, {RemoteID: "DRAFT", Name: "Drafts", Kind: models.FolderDrafts},
			{RemoteID: gmail.AllMail, Name: "All Mail", Kind: models.FolderArchive}, {RemoteID: "SPAM", Name: "Spam", Kind: models.FolderSpam},
			{RemoteID: "TRASH", Name: "Trash", Kind: models.FolderTrash},
		}
		p.labels = []mail.Label{{RemoteID: "Label_1", Name: "Work", Color: "#4986e7"}, {RemoteID: "Label_2", Name: "Finance", Color: "#16a766"},
			{RemoteID: "Label_3", Name: "Travel", Color: "#ffad47"}, {RemoteID: "Label_4", Name: "Family", Color: "#f691b3"}}
		return
	}
	p.folders = []mail.Folder{
		{RemoteID: "inbox", Name: "Inbox", Kind: models.FolderInbox}, {RemoteID: "sentitems", Name: "Sent Items", Kind: models.FolderSent},
		{RemoteID: "drafts", Name: "Drafts", Kind: models.FolderDrafts}, {RemoteID: "archive", Name: "Archive", Kind: models.FolderArchive},
		{RemoteID: "junkemail", Name: "Junk Email", Kind: models.FolderSpam}, {RemoteID: "deleteditems", Name: "Deleted Items", Kind: models.FolderTrash},
		{RemoteID: "projects", Name: "Projects", Kind: models.FolderCustom}, {RemoteID: outlook.Flagged, Name: "Flagged", Kind: models.FolderStarred},
	}
	p.labels = []mail.Label{{RemoteID: "Work", Name: "Work", Color: outlook.PresetColor("preset7")}, {RemoteID: "Finance", Name: "Finance", Color: outlook.PresetColor("preset4")},
		{RemoteID: "Travel", Name: "Travel", Color: outlook.PresetColor("preset1")}, {RemoteID: "Family", Name: "Family", Color: outlook.PresetColor("preset9")}}
}

func (p *Provider) folderID(kind string) string {
	for _, f := range p.folders {
		if f.Kind == kind {
			return f.RemoteID
		}
	}
	return ""
}

func (p *Provider) folderSet(kind string) []string {
	id := p.folderID(kind)
	if p.isGmail() && kind != models.FolderSpam && kind != models.FolderTrash {
		if kind == models.FolderArchive {
			return []string{gmail.AllMail}
		}
		return []string{id, gmail.AllMail}
	}
	return []string{id}
}

func (p *Provider) nextID() string {
	p.seq++
	if p.isGmail() {
		return fmt.Sprintf("18f%013x", 0xa000000+p.seq)
	}
	return fmt.Sprintf("AAMkAGI2TG93AAA%06d=", p.seq)
}

// seed generates ~60 messages spread over the last 30 days.
func (p *Provider) seed(now time.Time) {
	type item struct {
		t       template
		age     time.Duration
		variant int
	}
	var items []item
	n := 0
	for round := 0; n < 52; round++ {
		for i, t := range templates {
			if round > 0 && (t.replies != nil || t.folder != "") {
				continue // threads and spam only once
			}
			if round > 0 && (i+round)%3 == 0 {
				continue
			}
			age := time.Duration(n)*13*time.Hour + time.Duration(p.rng.IntN(300))*time.Minute
			items = append(items, item{t, age, round})
			n++
			if n >= 52 {
				break
			}
		}
	}
	for _, it := range items {
		t := it.t
		if it.variant > 0 {
			t.subject = variantSubject(t.subject, it.variant)
		}
		date := now.Add(-it.age)
		folder := models.FolderInbox
		switch {
		case t.folder == "spam":
			folder = models.FolderSpam
		case it.age > 18*24*time.Hour && p.rng.IntN(3) == 0:
			folder = models.FolderArchive
		case !p.isGmail() && containsLabel(t.labels, "Work") && p.rng.IntN(3) == 0:
			folder = models.FolderCustom
		}
		unread := it.age < 3*24*time.Hour || p.rng.IntN(5) == 0
		id := p.add(t, p.me, date, p.folderSet(folder), unread, t.starred && it.variant == 0, nil)
		// Threads: alternate replies between me and the sender.
		prev := p.msgs[id]
		for i, r := range t.replies {
			from := p.me
			to := models.Address{Name: t.fromName, Email: t.fromEmail}
			folderSet := p.folderSet(models.FolderSent)
			if i%2 == 1 {
				from, to = to, p.me
				folderSet = p.folderSet(folder)
			}
			rt := template{fromName: from.Name, fromEmail: from.Email, subject: mailmime.ReplySubject(t.subject), paragraphs: []string{r}, labels: t.labels}
			nid := p.add(rt, to, date.Add(time.Duration(i+1)*47*time.Minute), folderSet, i%2 == 1 && unread, false, prev)
			prev = p.msgs[nid]
		}
	}
	// Sent, drafts and trash.
	for i, t := range sentTemplates {
		to := []models.Address{{Name: "Mark Jansen", Email: "mark@contoso.example"}, {Name: "Dad", Email: "henk.visser@example.nl"}, {Name: "Finance", Email: "finance@northwind.io"}}[i%3]
		t.fromName, t.fromEmail = p.me.Name, p.me.Email
		p.add(t, to, now.Add(-time.Duration(i*61+20)*time.Hour), p.folderSet(models.FolderSent), false, false, nil)
	}
	for _, t := range draftTemplates {
		t.fromName, t.fromEmail = p.me.Name, p.me.Email
		p.add(t, models.Address{}, now.Add(-5*time.Hour), p.folderSet(models.FolderDrafts), false, false, nil)
	}
	trash := template{fromName: "Daily Deals", fromEmail: "deals@shop.example", subject: "Last chance: 70% off everything", paragraphs: []string{"Ends tonight!"}, newsletter: true}
	p.add(trash, p.me, now.Add(-9*24*time.Hour), p.folderSet(models.FolderTrash), false, false, nil)
	p.version = 1
	p.changes = nil
}

func containsLabel(ls []string, l string) bool {
	for _, x := range ls {
		if x == l {
			return true
		}
	}
	return false
}

func variantSubject(s string, v int) string {
	repl := []struct{ a, b string }{{"September", "August"}, {"Q4", "Q3"}, {"this week", "last week"}, {"14 Nov", "2 Dec"}, {"Sunday", "Saturday"}}
	for _, r := range repl {
		if strings.Contains(s, r.a) {
			return strings.Replace(s, r.a, r.b, 1)
		}
	}
	return s + " (#" + strconv.Itoa(v+1) + ")"
}

// add builds the raw MIME for a template and stores the message.
func (p *Provider) add(t template, to models.Address, date time.Time, folders []string, unread, starred bool, parent *message) string {
	id := p.nextID()
	from := models.Address{Name: t.fromName, Email: t.fromEmail}
	cid := ""
	var atts []mailmime.Attachment
	if t.newsletter {
		cid = "logo-" + id + "@demo"
		atts = append(atts, mailmime.Attachment{Filename: "logo.png", MimeType: "image/png", Data: tinyPNG, ContentID: cid})
	}
	for _, a := range t.attachments {
		atts = append(atts, mailmime.Attachment{Filename: a.name, MimeType: a.mime, Data: a.data()})
	}
	body := htmlBody(t, cid)
	if t.rawHTML != "" {
		body = t.rawHTML
	} else if parent != nil && len(t.paragraphs) == 1 {
		body = replyHTML(t.paragraphs[0])
	}
	m := mailmime.Message{From: from, Subject: t.subject, HTML: body, Attachments: atts, Date: date}
	if to.Email != "" {
		m.To = []models.Address{to}
	}
	thread := id
	if parent != nil {
		m.InReplyTo = parent.meta.MessageID
		m.References = mailmime.ReferencesFor(parent.meta.References, parent.meta.MessageID)
		thread = parent.meta.ThreadID
	}
	m.MessageID = "<" + id + "@demo.gomail>"
	raw, err := mailmime.Build(m)
	if err != nil {
		panic(err) // static demo data
	}
	text := mailmime.HTMLToText(body)
	hasAtt := len(t.attachments) > 0
	meta := mail.Message{
		RemoteID: id, ThreadID: thread, FolderRemoteIDs: folders, From: from, To: m.To, Subject: t.subject,
		Snippet: mailmime.Snippet(text, 140), Date: date.UTC(), Unread: unread, Starred: starred, HasAttachments: hasAtt,
		MessageID: m.MessageID, InReplyTo: m.InReplyTo, References: m.References, Labels: append([]string(nil), t.labels...),
	}
	p.msgs[id] = &message{meta: meta, raw: raw}
	p.touch(id, false)
	return id
}

func (p *Provider) touch(id string, deleted bool) {
	p.version++
	p.changes = append(p.changes, change{version: p.version, id: id, deleted: deleted})
}

func (p *Provider) sleep(ctx context.Context) error {
	if p.Latency <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.Latency):
		return nil
	}
}

// ---- provider.Provider ----

func (p *Provider) Profile(ctx context.Context) (provider.Profile, error) {
	return provider.Profile{Email: p.me.Email, Name: p.me.Name}, p.sleep(ctx)
}

func (p *Provider) Folders(ctx context.Context) ([]mail.Folder, []mail.Label, error) {
	if err := p.sleep(ctx); err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]mail.Folder, len(p.folders))
	copy(out, p.folders)
	for i := range out {
		for _, m := range p.msgs {
			if contains(m.meta.FolderRemoteIDs, out[i].RemoteID) {
				out[i].Total++
				if m.meta.Unread {
					out[i].Unread++
				}
			}
		}
	}
	return out, append([]mail.Label(nil), p.labels...), nil
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func (p *Provider) snapshot(id string) mail.Message {
	m := p.msgs[id].meta
	m.FolderRemoteIDs = append([]string(nil), m.FolderRemoteIDs...)
	m.Labels = append([]string(nil), m.Labels...)
	return m
}

func (p *Provider) Sync(ctx context.Context, req provider.SyncRequest) (mail.SyncResult, error) {
	if err := p.sleep(ctx); err != nil {
		return mail.SyncResult{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	inst, verStr, _ := strings.Cut(req.State["cursor"], ":")
	ver, err := strconv.Atoi(verStr)
	if inst != p.instance || err != nil {
		res := mail.SyncResult{Initial: true}
		for id := range p.msgs {
			res.Upserts = append(res.Upserts, p.snapshot(id))
		}
		sort.Slice(res.Upserts, func(i, j int) bool { return res.Upserts[i].Date.After(res.Upserts[j].Date) })
		res.State = map[string]string{"cursor": p.instance + ":" + strconv.Itoa(p.version)}
		return res, nil
	}
	if p.rng.Float64() < p.NewMailChance {
		p.deliverRandom()
	}
	res := mail.SyncResult{}
	seen := map[string]bool{}
	for _, c := range p.changes {
		if c.version <= ver || seen[c.id] {
			continue
		}
		seen[c.id] = true
		if _, ok := p.msgs[c.id]; !ok {
			res.Deleted = append(res.Deleted, c.id)
			continue
		}
		res.Upserts = append(res.Upserts, p.snapshot(c.id))
	}
	res.State = map[string]string{"cursor": p.instance + ":" + strconv.Itoa(p.version)}
	return res, nil
}

var incoming = []template{
	{fromName: "Priya Raman", fromEmail: "priya.raman@northwind.io", subject: "Quick question about the release notes", paragraphs: []string{"Hey, do you have 5 minutes to look at the release notes draft? I want to send them out this afternoon."}, labels: []string{"Work"}},
	{fromName: "PostNL", fromEmail: "noreply@postnl.example", subject: "Your parcel will be delivered today", paragraphs: []string{"Expected between 13:30 and 15:30."}},
	{fromName: "Acme Cloud Billing", fromEmail: "billing@acmecloud.com", subject: "Payment received — thank you", paragraphs: []string{"We received your payment of $248.17."}, labels: []string{"Finance"}},
	{fromName: "Sofia Martins", fromEmail: "sofia.martins@example.pt", subject: "Weather in Lisbon next week ☀️", paragraphs: []string{"Good news, 22°C and sunny all week!"}, labels: []string{"Travel"}},
	{fromName: "The Morning Brew", fromEmail: "crew@morningbrew.example", subject: "☕ The Fed holds rates steady", paragraphs: []string{"Plus: the best gadgets of the year."}, newsletter: true},
}

// deliverRandom simulates a new incoming message (caller holds p.mu).
func (p *Provider) deliverRandom() {
	t := incoming[p.rng.IntN(len(incoming))]
	p.add(t, p.me, time.Now(), p.folderSet(models.FolderInbox), true, false, nil)
}

// Deliver adds a new incoming message now (tests).
func (p *Provider) Deliver(subject, fromEmail string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := template{fromName: fromEmail, fromEmail: fromEmail, subject: subject, paragraphs: []string{"Hello from the demo provider."}}
	return p.add(t, p.me, time.Now(), p.folderSet(models.FolderInbox), true, false, nil)
}

func (p *Provider) get(id string) (*message, error) {
	m, ok := p.msgs[id]
	if !ok {
		return nil, errors.New("message not found")
	}
	return m, nil
}

func (p *Provider) Body(ctx context.Context, remoteID string) (*mail.Body, error) {
	if err := p.sleep(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	m, err := p.get(remoteID)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	parsed, err := mailmime.Parse(m.raw)
	if err != nil {
		return nil, err
	}
	b := &mail.Body{HTML: parsed.HTML, Text: parsed.Text, MessageID: parsed.MessageID, InReplyTo: parsed.InReplyTo, References: parsed.References}
	for _, a := range parsed.Attachments {
		att := mail.Attachment{Attachment: models.Attachment{ID: a.ID, Filename: a.Filename, MimeType: a.ContentType, Size: int64(len(a.Data)), Inline: a.Inline, ContentID: a.ContentID}}
		if a.ContentID != "" {
			att.Data = a.Data
		}
		b.Attachments = append(b.Attachments, att)
	}
	return b, nil
}

func (p *Provider) Attachment(ctx context.Context, remoteID, attachmentID string) ([]byte, error) {
	if err := p.sleep(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	m, err := p.get(remoteID)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	parsed, err := mailmime.Parse(m.raw)
	if err != nil {
		return nil, err
	}
	for _, a := range parsed.Attachments {
		if a.ID == attachmentID {
			return a.Data, nil
		}
	}
	return nil, errors.New("attachment not found")
}

func (p *Provider) update(ctx context.Context, ids []string, f func(*mail.Message)) error {
	if err := p.sleep(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range ids {
		if m, ok := p.msgs[id]; ok {
			f(&m.meta)
			p.touch(id, false)
		}
	}
	return nil
}

func (p *Provider) SetRead(ctx context.Context, ids []string, read bool) error {
	return p.update(ctx, ids, func(m *mail.Message) { m.Unread = !read })
}

func (p *Provider) SetStarred(ctx context.Context, ids []string, starred bool) error {
	return p.update(ctx, ids, func(m *mail.Message) { m.Starred = starred })
}

func (p *Provider) PlanMove(current []string, target mail.Folder) []string {
	if p.isGmail() {
		return (&gmail.Client{}).PlanMove(current, target)
	}
	return (&outlook.Client{}).PlanMove(current, target)
}

func (p *Provider) Move(ctx context.Context, ids []string, target mail.Folder) error {
	if target.Kind == models.FolderStarred {
		return p.SetStarred(ctx, ids, true)
	}
	if target.Kind == models.FolderSent || (p.isGmail() && target.Kind == models.FolderDrafts) {
		return fmt.Errorf("messages can't be moved to %s: %w", target.Name, provider.ErrUnsupported)
	}
	return p.update(ctx, ids, func(m *mail.Message) { m.FolderRemoteIDs = p.PlanMove(m.FolderRemoteIDs, target) })
}

func (p *Provider) SetLabel(ctx context.Context, ids []string, name string, add bool) error {
	p.mu.Lock()
	known := false
	for _, l := range p.labels {
		if strings.EqualFold(l.Name, name) {
			known, name = true, l.Name
		}
	}
	if !known && add {
		p.labels = append(p.labels, mail.Label{RemoteID: name, Name: name})
	}
	p.mu.Unlock()
	return p.update(ctx, ids, func(m *mail.Message) {
		var out []string
		for _, l := range m.Labels {
			if !strings.EqualFold(l, name) {
				out = append(out, l)
			}
		}
		if add {
			out = append(out, name)
		}
		m.Labels = out
	})
}

func (p *Provider) Send(ctx context.Context, out mail.Outgoing) error {
	if err := p.sleep(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var parent *message
	if out.Original != nil {
		parent = p.msgs[out.Original.RemoteID]
		if out.Mode == "forward" {
			parent = nil
		}
	}
	html := out.HTML
	if html == "" {
		html = mailmime.TextToHTML(out.Text)
	}
	to := models.Address{}
	if len(out.To) > 0 {
		to = out.To[0]
	}
	t := template{fromName: p.me.Name, fromEmail: p.me.Email, subject: out.Subject, rawHTML: html}
	for _, a := range out.Attachments {
		data := a.Data
		t.attachments = append(t.attachments, tmplAttachment{a.Filename, a.MimeType, func() []byte { return data }})
	}
	p.add(t, to, time.Now(), p.folderSet(models.FolderSent), false, false, parent)
	// Mail to yourself arrives in the inbox too.
	for _, r := range append(append([]models.Address{}, out.To...), out.Cc...) {
		if strings.EqualFold(r.Email, p.me.Email) {
			p.add(t, p.me, time.Now(), p.folderSet(models.FolderInbox), true, false, parent)
			break
		}
	}
	return nil
}

var _ provider.Provider = (*Provider)(nil)
