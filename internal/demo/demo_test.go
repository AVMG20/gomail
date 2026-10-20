package demo

import (
	"context"
	"testing"
	"time"

	"github.com/AVMG20/gomail/internal/calendar"
	"github.com/AVMG20/gomail/internal/mail"
	"github.com/AVMG20/gomail/internal/models"
	"github.com/AVMG20/gomail/internal/provider"
)

func folderOf(t *testing.T, folders []mail.Folder, kind string) mail.Folder {
	t.Helper()
	for _, f := range folders {
		if f.Kind == kind {
			return f
		}
	}
	t.Fatalf("no %s folder", kind)
	return mail.Folder{}
}

// TestProviderSmoke runs every provider operation against both demo
// mailboxes.
func TestProviderSmoke(t *testing.T) {
	ctx := context.Background()
	for _, acct := range Accounts() {
		t.Run(acct.Provider, func(t *testing.T) {
			p := New(acct)
			p.Latency, p.NewMailChance = time.Millisecond, 0
			prof, err := p.Profile(ctx)
			if err != nil || prof.Email != acct.Email {
				t.Fatalf("profile %+v %v", prof, err)
			}
			folders, labels, err := p.Folders(ctx)
			if err != nil || len(folders) < 5 || len(labels) == 0 {
				t.Fatalf("folders %d labels %d %v", len(folders), len(labels), err)
			}
			inbox := folderOf(t, folders, models.FolderInbox)
			if inbox.Total == 0 || inbox.Unread == 0 {
				t.Fatalf("inbox %+v", inbox)
			}
			var progress int
			res, err := p.Sync(ctx, provider.SyncRequest{Folders: folders, Progress: func(string, int, int) { progress++ }})
			if err != nil || !res.Initial || len(res.Upserts) < 30 || res.State["cursor"] == "" || progress == 0 {
				t.Fatalf("initial sync %d %v", len(res.Upserts), err)
			}
			state := res.State

			// Bodies, inline images and attachments parse.
			var withAtt mail.Message
			for _, m := range res.Upserts {
				if m.HasAttachments {
					withAtt = m
					break
				}
			}
			b, err := p.Body(ctx, withAtt.RemoteID)
			if err != nil || b.HTML == "" || len(b.Attachments) == 0 {
				t.Fatalf("body %v", err)
			}
			data, err := p.Attachment(ctx, withAtt.RemoteID, b.Attachments[0].ID)
			if err != nil || len(data) == 0 {
				t.Fatalf("attachment %v", err)
			}
			if _, err := p.Attachment(ctx, withAtt.RemoteID, "nope"); err == nil {
				t.Fatal("missing attachment")
			}
			if _, err := p.Body(ctx, "nope"); err == nil {
				t.Fatal("missing body")
			}

			// Changes show up in the next incremental round.
			id := p.Deliver("Hello", "x@example.com")
			first := res.Upserts[0].RemoteID
			must(t, p.SetRead(ctx, []string{first}, true))
			must(t, p.SetStarred(ctx, []string{first}, true))
			must(t, p.SetLabel(ctx, []string{first}, "Brand new", true))
			archive := folderOf(t, folders, models.FolderArchive)
			must(t, p.Move(ctx, []string{first}, archive))
			if err := p.Move(ctx, []string{first}, folderOf(t, folders, models.FolderSent)); err == nil {
				t.Fatal("moved to sent")
			}
			res, err = p.Sync(ctx, provider.SyncRequest{State: state, Folders: folders})
			if err != nil || res.Initial || len(res.Upserts) != 2 {
				t.Fatalf("incremental %d %v", len(res.Upserts), err)
			}
			for _, m := range res.Upserts {
				if m.RemoteID == first && (m.Unread || !m.Starred || !containsLabel(m.Labels, "Brand new") || contains(m.FolderRemoteIDs, inbox.RemoteID)) {
					t.Fatalf("changed message %+v", m)
				}
				if m.RemoteID == id && !m.Unread {
					t.Fatal("delivered mail is read")
				}
			}
			state = res.State

			// Send, drafts and forwards.
			out := mail.Outgoing{From: models.Address{Name: prof.Name, Email: prof.Email}, To: []models.Address{{Email: acct.Email}}, Subject: "To myself", Text: "hi",
				Attachments: []mail.OutgoingAttachment{{Filename: "a.txt", MimeType: "text/plain", Data: []byte("x")}}}
			must(t, p.Send(ctx, out))
			did, err := p.SaveDraft(ctx, out, "")
			if err != nil || did == "" {
				t.Fatal(err)
			}
			if again, _ := p.SaveDraft(ctx, out, did); again != did {
				t.Fatal("draft id changed")
			}
			if got, _ := p.DraftID(ctx, did); got != did {
				t.Fatal("draft id lookup")
			}
			must(t, p.SendDraft(ctx, out, did))
			d2, _ := p.SaveDraft(ctx, out, "")
			must(t, p.DeleteDraft(ctx, d2))
			fwd := withAtt
			must(t, p.Send(ctx, mail.Outgoing{To: []models.Address{{Email: "y@example.com"}}, Subject: "Fwd", Mode: "forward", Original: &fwd}))
			res, err = p.Sync(ctx, provider.SyncRequest{State: state, Folders: folders})
			if err != nil || len(res.Upserts) < 3 || len(res.Deleted) == 0 {
				t.Fatalf("after send: %d upserts %d deleted %v", len(res.Upserts), len(res.Deleted), err)
			}

			// Older mail, search and folder operations.
			older, err := p.ListOlder(ctx, inbox, time.Now().Add(-31*24*time.Hour), 5)
			if err != nil || len(older) == 0 || len(older) > 5 {
				t.Fatalf("older %d %v", len(older), err)
			}
			hits, err := p.Search(ctx, mail.SearchQuery{Terms: []string{"Hello"}}, 10)
			if err != nil || len(hits) == 0 {
				t.Fatalf("search %d %v", len(hits), err)
			}
			if hits, _ := p.Search(ctx, mail.SearchQuery{}, 10); len(hits) != 0 {
				t.Fatal("empty search matched")
			}
			must(t, p.MarkFolderRead(ctx, inbox))
			folders, _, _ = p.Folders(ctx)
			if folderOf(t, folders, models.FolderInbox).Unread != 0 {
				t.Fatal("inbox still unread")
			}
			trash := folderOf(t, folders, models.FolderTrash)
			if err := p.EmptyFolder(ctx, inbox); err == nil {
				t.Fatal("emptied the inbox")
			}
			must(t, p.EmptyFolder(ctx, trash))
			folders, _, _ = p.Folders(ctx)
			if folderOf(t, folders, models.FolderTrash).Total != 0 {
				t.Fatal("trash not empty")
			}

			// Cancellation is honoured.
			cctx, cancel := context.WithCancel(ctx)
			cancel()
			if _, _, err := p.Folders(cctx); err == nil {
				t.Fatal("cancelled context ignored")
			}
		})
	}
}

func TestSizedAndVariants(t *testing.T) {
	p := NewSized(Accounts()[0], 500)
	p.Latency = 0
	res, err := p.Sync(context.Background(), provider.SyncRequest{})
	if err != nil || len(res.Upserts) < 500 {
		t.Fatalf("sized %d %v", len(res.Upserts), err)
	}
	if variantSubject("Due Sunday", 0) != "Due Saturday" || variantSubject("Hello", 3) != "Hello (#4)" {
		t.Fatal("variantSubject")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestCalendarProvider(t *testing.T) {
	ctx := context.Background()
	for _, acct := range Accounts() {
		p := NewCalendar(acct)
		cals, err := p.Calendars(ctx)
		if err != nil || len(cals) != 2 || !cals[0].Primary {
			t.Fatalf("%+v %v", cals, err)
		}
		start, end := time.Now().AddDate(0, 0, -7), time.Now().AddDate(0, 0, 35)
		total, allDay, recurring, declined := 0, 0, 0, 0
		for _, c := range cals {
			res, err := p.Sync(ctx, c, calendar.SyncRequest{Start: start, End: end})
			if err != nil || !res.Full {
				t.Fatal(err)
			}
			for _, ev := range res.Upserts {
				total++
				if ev.AllDay {
					allDay++
				}
				if ev.SeriesRemoteID != "" {
					recurring++
				}
				if ev.MyResponse == models.ResponseDeclined {
					declined++
				}
			}
		}
		if total < 40 || allDay < 3 || recurring < 20 || declined != 1 {
			t.Fatalf("%s: total %d all-day %d recurring %d declined %d", acct.Provider, total, allDay, recurring, declined)
		}
		// CRUD in memory.
		work := cals[1]
		ev, err := p.Create(ctx, work, calendar.Event{Title: "New", Start: time.Now(), End: time.Now().Add(time.Hour), RRule: "FREQ=DAILY;COUNT=3"})
		if err != nil || !ev.IsRecurring {
			t.Fatal(err)
		}
		evs, _ := p.Range(ctx, work, time.Now().Add(-time.Hour), time.Now().AddDate(0, 0, 5))
		var occ []calendar.Event
		for _, e := range evs {
			if e.SeriesRemoteID == ev.RemoteID {
				occ = append(occ, e)
			}
		}
		if len(occ) != 3 {
			t.Fatalf("%d occurrences", len(occ))
		}
		if _, err := p.Update(ctx, work, occ[0].RemoteID, calendar.Event{Title: "Moved", Start: occ[0].Start.Add(time.Hour), End: occ[0].End.Add(time.Hour)}, models.ScopeSeries); err != nil {
			t.Fatal(err)
		}
		if err := p.Respond(ctx, work, occ[0].RemoteID, models.ResponseAccepted); err != nil {
			t.Fatal(err)
		}
		if err := p.Delete(ctx, work, occ[1].RemoteID, models.ScopeSeries); err != nil {
			t.Fatal(err)
		}
		evs, _ = p.Range(ctx, work, time.Now().Add(-time.Hour), time.Now().AddDate(0, 0, 5))
		for _, e := range evs {
			if e.SeriesRemoteID == ev.RemoteID {
				t.Fatal("series not deleted")
			}
		}
		if _, err := p.Update(ctx, work, "missing", calendar.Event{}, models.ScopeThis); err == nil {
			t.Fatal("updated a missing event")
		}
	}
}
