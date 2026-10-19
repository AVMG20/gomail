// Seed data for the in-browser mock backend. Deterministic so screenshots and
// tests are stable between runs.
import type { Account, Address, Attachment, Label, Folder, Message, Settings, AIModel } from '../types'

export const DEFAULT_GRAMMAR_PROMPT =
  'You are a meticulous copy editor. Fix spelling, grammar, punctuation and awkward phrasing in the text the user sends. ' +
  'Keep the original meaning, tone, language and formatting (including any HTML tags). Do not add new content. ' +
  'Reply with the corrected text only.'

export const DEFAULT_CLASSIFY_INSTRUCTIONS =
  'Assign the single best-fitting label to each email based on its sender, subject and content. ' +
  'Prefer "none" when no label clearly applies.'

export const SEED_LABELS: Label[] = [
  { id: 'work', name: 'Work', description: 'Colleagues, projects, meetings and anything job related.', color: '#3b82f6' },
  { id: 'finance', name: 'Finance', description: 'Invoices, receipts, bank statements, payments and taxes.', color: '#22c55e' },
  { id: 'travel', name: 'Travel', description: 'Flights, hotels, bookings and itineraries.', color: '#f97316' },
  { id: 'newsletters', name: 'Newsletters', description: 'Newsletters, digests, product announcements and marketing.', color: '#8b5cf6' },
  { id: 'social', name: 'Personal', description: 'Friends and family, personal conversations and social invites.', color: '#ec4899' },
  { id: 'updates', name: 'Notifications', description: 'Automated notifications from apps and services (GitHub, calendars, security alerts).', color: '#64748b' },
]

export const SEED_SETTINGS: Settings = {
  theme: 'system',
  density: 'comfortable',
  syncIntervalMinutes: 5,
  showRemoteImages: false,
  googleClientId: '1234567890-demo.apps.googleusercontent.com',
  googleClientSecret: '',
  microsoftClientId: '',
  microsoftTenant: 'common',
  openRouterKeySet: true,
  openRouterModel: 'openai/gpt-4o-mini',
  classifierModel: 'typesafe/jev-1.13',
  classifyThreshold: 0.5,
  classifyInstructions: DEFAULT_CLASSIFY_INSTRUCTIONS,
  autoClassify: true,
  labels: SEED_LABELS,
  primaryColor: '#2563eb',
  radius: 0.625,
  grammarSystemPrompt: DEFAULT_GRAMMAR_PROMPT,
}

const now = Date.now()
const HOUR = 3600_000

export const SEED_ACCOUNTS: Account[] = [
  {
    id: 'acc_personal', provider: 'gmail', email: 'alex.morgan@gmail.com', displayName: 'Personal',
    color: '#3b82f6', createdAt: new Date(now - 400 * 24 * HOUR).toISOString(),
    lastSyncAt: new Date(now - 4 * 60_000).toISOString(), syncError: '', syncing: false, unreadCount: 0,
  },
  {
    id: 'acc_work', provider: 'outlook', email: 'alex.morgan@northwind.io', displayName: 'Northwind',
    color: '#f59e0b', createdAt: new Date(now - 200 * 24 * HOUR).toISOString(),
    lastSyncAt: new Date(now - 2 * 60_000).toISOString(), syncError: '', syncing: false, unreadCount: 0,
  },
  {
    id: 'acc_side', provider: 'gmail', email: 'hello@pixelforge.studio', displayName: 'Pixelforge',
    color: '#14b8a6', createdAt: new Date(now - 90 * 24 * HOUR).toISOString(),
    lastSyncAt: new Date(now - 11 * 60_000).toISOString(), syncError: '', syncing: false, unreadCount: 0,
  },
]

const SYSTEM_FOLDERS: [string, string][] = [
  ['inbox', 'Inbox'], ['starred', 'Starred'], ['sent', 'Sent'], ['drafts', 'Drafts'],
  ['archive', 'Archive'], ['spam', 'Spam'], ['trash', 'Trash'],
]

const CUSTOM_FOLDERS: Record<string, string[]> = {
  acc_personal: ['Receipts', 'Family', 'Travel plans'],
  acc_work: ['Atlas project', 'Clients', 'Hiring'],
  acc_side: ['Customers', 'Invoices'],
}

export function foldersFor(accountId: string, provider: string): Folder[] {
  const list: Folder[] = SYSTEM_FOLDERS.map(([kind, name]) => ({
    id: `${accountId}:${kind}`, accountId, remoteId: provider === 'gmail' ? kind.toUpperCase() : name,
    name: provider === 'outlook' && kind === 'trash' ? 'Deleted Items' : provider === 'outlook' && kind === 'spam' ? 'Junk Email' : name,
    kind, unread: 0, total: 0,
  }))
  for (const name of CUSTOM_FOLDERS[accountId] ?? []) {
    list.push({ id: `${accountId}:label:${name.toLowerCase().replace(/\s+/g, '-')}`, accountId, remoteId: name, name, kind: 'custom', unread: 0, total: 0 })
  }
  return list
}

// ---------------------------------------------------------------------------
// Message templates

interface Tpl {
  acc: string
  from: Address
  subject: string
  text?: string
  html?: string
  cat?: string
  /** Extra labels besides `cat`. */
  more?: string[]
  att?: Omit<Attachment, 'id' | 'inline' | 'contentId'>[]
  folder?: string // kind or label id suffix
  starred?: boolean
  read?: boolean
  /** Extra replies in the same thread: [from, text] */
  replies?: [Address, string][]
  to?: Address[]
  cc?: Address[]
}

const me = {
  acc_personal: { name: 'Alex Morgan', email: 'alex.morgan@gmail.com' },
  acc_work: { name: 'Alex Morgan', email: 'alex.morgan@northwind.io' },
  acc_side: { name: 'Pixelforge Studio', email: 'hello@pixelforge.studio' },
} as Record<string, Address>

const P = {
  priya: { name: 'Priya Raman', email: 'priya.raman@northwind.io' },
  jonas: { name: 'Jonas Weber', email: 'jonas.weber@northwind.io' },
  mara: { name: 'Mara Lindqvist', email: 'mara@northwind.io' },
  sam: { name: 'Sam Okafor', email: 'sam.okafor@gmail.com' },
  mom: { name: 'Mom', email: 'linda.morgan@icloud.com' },
  leo: { name: 'Leo Chen', email: 'leo.chen@fastmail.com' },
  ines: { name: 'Inês Duarte', email: 'ines@brightlane.co' },
  github: { name: 'GitHub', email: 'notifications@github.com' },
  stripe: { name: 'Stripe', email: 'receipts@stripe.com' },
  klm: { name: 'KLM Royal Dutch Airlines', email: 'noreply@klm.com' },
  booking: { name: 'Booking.com', email: 'customer.service@booking.com' },
  figma: { name: 'Figma', email: 'no-reply@figma.com' },
  linear: { name: 'Linear', email: 'notifications@linear.app' },
  bank: { name: 'Bunq', email: 'statements@bunq.com' },
  verge: { name: 'The Verge', email: 'newsletter@theverge.com' },
  tldr: { name: 'TLDR', email: 'dan@tldrnewsletter.com' },
  calendar: { name: 'Google Calendar', email: 'calendar-notification@google.com' },
  amazon: { name: 'Amazon.de', email: 'shipment-tracking@amazon.de' },
  hr: { name: 'Northwind People Team', email: 'people@northwind.io' },
  client: { name: 'Daniel Brooks', email: 'daniel@acmeoutdoor.com' },
  sec: { name: 'Google', email: 'no-reply@accounts.google.com' },
  vercel: { name: 'Vercel', email: 'notifications@vercel.com' },
  airbnb: { name: 'Airbnb', email: 'automated@airbnb.com' },
  spotify: { name: 'Spotify', email: 'no-reply@spotify.com' },
  notion: { name: 'Notion Team', email: 'team@makenotion.com' },
  hn: { name: 'Hacker Newsletter', email: 'kale@hackernewsletter.com' },
  rachel: { name: 'Rachel Kim', email: 'rachel.kim@lumenlabs.io' },
  tomas: { name: 'Tomás Rivera', email: 'tomas@riveradesign.mx' },
  dentist: { name: 'Smile Dental Clinic', email: 'appointments@smiledental.nl' },
}

const newsletterHtml = (title: string, items: [string, string][], accent: string, img = true) => `
<div style="background:#f4f4f5;padding:24px 0;font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif">
  <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:600px;margin:0 auto;background:#ffffff;border-radius:12px;overflow:hidden">
    <tr><td style="background:${accent};padding:28px 32px;color:#fff">
      <div style="font-size:13px;letter-spacing:.08em;text-transform:uppercase;opacity:.85">Weekly digest</div>
      <div style="font-size:26px;font-weight:700;margin-top:6px">${title}</div>
    </td></tr>
    ${img ? `<tr><td><img src="https://images.unsplash.com/photo-1518770660439-4636190af475?w=1200" width="600" alt="Header image" style="display:block;width:100%;height:auto"></td></tr>` : ''}
    ${items.map(([h, p]) => `<tr><td style="padding:22px 32px 4px"><a href="https://example.com/${encodeURIComponent(h.toLowerCase().replace(/\s+/g, '-'))}" style="color:#111827;font-size:18px;font-weight:600;text-decoration:none">${h}</a>
      <p style="color:#4b5563;font-size:15px;line-height:1.6;margin:8px 0 0">${p}</p></td></tr>`).join('')}
    <tr><td style="padding:28px 32px"><a href="https://example.com/read" style="display:inline-block;background:${accent};color:#fff;padding:12px 20px;border-radius:8px;text-decoration:none;font-weight:600">Read the full issue</a></td></tr>
    <tr><td style="padding:16px 32px 28px;color:#9ca3af;font-size:12px">You are receiving this because you subscribed. <a href="https://example.com/unsubscribe" style="color:#9ca3af">Unsubscribe</a>
      <img src="https://tracking.example.com/open.gif?u=123" width="1" height="1" alt=""></td></tr>
  </table>
</div>`

const receiptHtml = (merchant: string, rows: [string, string][], total: string) => `
<div style="font-family:-apple-system,Segoe UI,Helvetica,Arial,sans-serif;max-width:560px;margin:0 auto;color:#1f2937">
  <h2 style="font-size:20px;margin:0 0 4px">Receipt from ${merchant}</h2>
  <p style="color:#6b7280;margin:0 0 20px">Thanks for your payment. Here is a summary.</p>
  <table width="100%" cellpadding="8" cellspacing="0" style="border-collapse:collapse;font-size:14px">
    ${rows.map(([a, b]) => `<tr><td style="border-bottom:1px solid #e5e7eb">${a}</td><td align="right" style="border-bottom:1px solid #e5e7eb">${b}</td></tr>`).join('')}
    <tr><td style="font-weight:700">Total</td><td align="right" style="font-weight:700">${total}</td></tr>
  </table>
  <p style="font-size:13px;color:#6b7280;margin-top:24px">Questions? Visit <a href="https://support.example.com">our support center</a>.</p>
  <script>alert('this script must never run')</script>
  <img src="x" onerror="alert('xss')" style="display:none">
</div>`

const T: Tpl[] = [
  // ---- work (Outlook) ----
  {
    acc: 'acc_work', from: P.priya, subject: 'Atlas Q3 roadmap — final review before Thursday', cat: 'work', starred: true, folder: 'label:atlas-project',
    text: `Hi Alex,\n\nI've pulled together the final version of the Atlas Q3 roadmap. The big changes since last week:\n\n1. Offline sync moves up to milestone 2 (customer feedback from Brightlane was loud and clear).\n2. We're dropping the custom reporting builder for now and shipping three templates instead.\n3. Launch date stays at September 18.\n\nCould you review the deck before Thursday's leadership sync? Comments directly in the doc are perfect: https://docs.northwind.io/atlas/q3-roadmap\n\nThanks!\nPriya`,
    att: [{ filename: 'Atlas-Q3-Roadmap.pdf', mimeType: 'application/pdf', size: 2_480_000 }, { filename: 'milestones.xlsx', mimeType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', size: 48_200 }],
    cc: [P.jonas, P.mara],
    replies: [
      [P.jonas, `Looks great. One question on milestone 2 — do we have capacity on the mobile team for offline sync, or are we borrowing from Platform?\n\nJonas`],
      [P.priya, `Good question. We're borrowing two engineers from Platform for six weeks; Mara already agreed. I'll add a note to the deck.`],
    ],
  },
  {
    acc: 'acc_work', from: P.jonas, subject: 'Re: Incident review — checkout latency spike', cat: 'work',
    text: `Hey team,\n\nPost-mortem for Tuesday's checkout latency spike is up. TL;DR: a missing index on orders.created_at after the migration. We've added the index, an alert on p95 > 800ms, and a migration checklist item.\n\nFull write-up: https://wiki.northwind.io/incidents/2024-117\n\nJonas`,
    replies: [[P.mara, `Thanks Jonas — really clear write-up. Can we also add a load test to the release pipeline?`]],
  },
  {
    acc: 'acc_work', from: P.mara, subject: 'Design crit moved to 3pm', cat: 'work', read: true,
    text: `Quick heads-up: the design crit moved from 2pm to 3pm today because of the all-hands overrun. Same room (Fjord), same agenda.\n\nMara`,
  },
  {
    acc: 'acc_work', from: P.hr, subject: 'Reminder: submit your H2 goals by Friday', cat: 'work', read: true,
    html: `<p>Hi Alex,</p><p>This is a friendly reminder to submit your <strong>H2 goals</strong> in Lattice by <strong>Friday, 5pm</strong>.</p><ul><li>3–5 goals, each with a measurable outcome</li><li>At least one goal focused on growth or learning</li><li>Align with your manager in your next 1:1</li></ul><p>Need help? Check the <a href="https://people.northwind.io/goals-guide">goal-setting guide</a>.</p><p>— People Team</p>`,
  },
  {
    acc: 'acc_work', from: P.client, subject: 'Contract renewal & pricing for 2025', cat: 'work', more: ['finance'], starred: true, folder: 'label:clients',
    text: `Hi Alex,\n\nWe're happy with how the pilot went and would like to discuss renewing for 2025 across all three regions. Could you send over updated pricing for ~240 seats, and let me know whether annual prepay changes the discount?\n\nAlso — is SSO included in the business tier or is that an add-on?\n\nBest,\nDaniel Brooks\nHead of Operations, Acme Outdoor`,
    att: [{ filename: 'Acme-seat-forecast.csv', mimeType: 'text/csv', size: 12_400 }],
  },
  {
    acc: 'acc_work', from: P.linear, subject: '[ATL-482] Offline queue drops edits after reconnect', cat: 'updates',
    html: `<div style="font-family:-apple-system,Segoe UI,sans-serif;font-size:14px;color:#222"><p><strong>Priya Raman</strong> assigned <a href="https://linear.app/northwind/issue/ATL-482">ATL-482</a> to you</p><blockquote style="border-left:3px solid #5e6ad2;margin:0;padding:4px 12px;color:#555">When the device reconnects after &gt;10 minutes offline, the queued edits are replayed out of order and the server rejects the stale ones.</blockquote><p style="color:#888;font-size:12px">Priority: High · Cycle 14 · Atlas</p></div>`,
  },
  {
    acc: 'acc_work', from: P.linear, subject: '[ATL-479] Add retry with exponential backoff', cat: 'updates', read: true,
    html: `<div style="font-family:-apple-system,Segoe UI,sans-serif;font-size:14px;color:#222"><p><strong>Jonas Weber</strong> moved <a href="https://linear.app/northwind/issue/ATL-479">ATL-479</a> to <strong>In Review</strong></p></div>`,
  },
  {
    acc: 'acc_work', from: P.ines, subject: 'Offline sync feedback from our field teams', cat: 'work',
    text: `Hi Alex,\n\nAs promised, here's the feedback from our field teams after two weeks with the beta. Overall very positive — the main ask is a clearer indicator of what hasn't synced yet.\n\nI've attached the survey export. Happy to jump on a call next week.\n\nInês\nBrightlane`,
    att: [{ filename: 'field-survey.pdf', mimeType: 'application/pdf', size: 845_000 }, { filename: 'screenshot-sync-state.png', mimeType: 'image/png', size: 312_000 }],
    folder: 'label:clients',
  },
  {
    acc: 'acc_work', from: P.hr, subject: 'Interview loop: Senior Frontend Engineer (Thu)', cat: 'work', folder: 'label:hiring', read: true,
    text: `Hi Alex,\n\nYou're on the interview loop for the Senior Frontend Engineer candidate on Thursday, 11:00–11:45 (system design). Scorecard and CV are in Greenhouse.\n\nThanks for helping!\nPeople Team`,
  },
  {
    acc: 'acc_work', from: { name: 'Priya Raman (via Outlook Calendar)', email: 'priya.raman@northwind.io' }, subject: 'Invitation: Atlas leadership sync @ Thu 10:00 – 11:00', cat: 'updates',
    html: `<div style="font-family:-apple-system,Segoe UI,sans-serif"><p><strong>Atlas leadership sync</strong></p><p>Thursday · 10:00 – 11:00 (CEST)<br>Microsoft Teams meeting</p><p><a href="https://teams.microsoft.com/l/meetup-join/abc">Join the meeting</a></p><p style="color:#666">Organizer: Priya Raman</p></div>`,
    att: [{ filename: 'invite.ics', mimeType: 'text/calendar', size: 2_100 }],
  },
  // ---- personal (Gmail) ----
  {
    acc: 'acc_personal', from: P.sam, subject: 'Climbing Saturday?', cat: 'social',
    text: `Yo! Bouldering gym on Saturday morning? They just reset the whole cave section. I can pick you up around 9.\n\nSam`,
    replies: [[me.acc_personal, `I'm in! 9 works. Bringing the new shoes 😄`], [P.sam, `Legend. See you then.`]],
  },
  {
    acc: 'acc_personal', from: P.mom, subject: 'Photos from the weekend', cat: 'social', starred: true, folder: 'label:family',
    text: `Hi sweetheart,\n\nHere are the photos from Sunday. Grandpa loved the cake! Let me know if you want the originals, the files are big.\n\nLove,\nMom`,
    att: [{ filename: 'IMG_2041.jpg', mimeType: 'image/jpeg', size: 3_200_000 }, { filename: 'IMG_2044.jpg', mimeType: 'image/jpeg', size: 2_950_000 }, { filename: 'IMG_2052.jpg', mimeType: 'image/jpeg', size: 3_410_000 }],
  },
  {
    acc: 'acc_personal', from: P.klm, subject: 'Your booking confirmation: Amsterdam → Lisbon', cat: 'travel', more: ['finance'], folder: 'label:travel-plans',
    html: `<div style="font-family:Arial,sans-serif;max-width:600px;margin:auto;color:#003145"><div style="background:#00a1de;color:#fff;padding:20px 24px;border-radius:8px 8px 0 0"><div style="font-size:22px;font-weight:bold">Booking confirmed</div><div>Booking code <strong>X7K2QP</strong></div></div><div style="border:1px solid #d6e6ef;border-top:0;padding:20px 24px;border-radius:0 0 8px 8px"><table width="100%" style="font-size:15px"><tr><td><strong>KL1693</strong><br>Amsterdam (AMS)<br>Fri 14 Jun · 07:05</td><td align="center">✈</td><td align="right">Lisbon (LIS)<br>Fri 14 Jun · 09:00</td></tr></table><p style="margin-top:16px">Passenger: Alex Morgan · Seat 14A · 1 checked bag</p><p><a href="https://www.klm.com/manage" style="color:#00a1de">Manage your booking</a></p></div></div>`,
    att: [{ filename: 'e-ticket-X7K2QP.pdf', mimeType: 'application/pdf', size: 156_000 }],
  },
  {
    acc: 'acc_personal', from: P.booking, subject: 'Confirmed: Casa do Alfama, 14–18 June', cat: 'travel', more: ['finance'], read: true, folder: 'label:travel-plans',
    html: `<div style="font-family:-apple-system,Arial,sans-serif;max-width:600px"><h2 style="color:#003580">Your booking is confirmed</h2><p><strong>Casa do Alfama</strong><br>Rua de São Miguel 12, Lisbon</p><table cellpadding="6" style="font-size:14px"><tr><td>Check-in</td><td><strong>Fri 14 June</strong> from 15:00</td></tr><tr><td>Check-out</td><td><strong>Tue 18 June</strong> until 11:00</td></tr><tr><td>Price</td><td><strong>€ 612</strong> (paid)</td></tr></table><p><img src="https://images.unsplash.com/photo-1585208798174-6cedd86e019a?w=800" width="560" alt="Lisbon" style="border-radius:8px;max-width:100%"></p></div>`,
  },
  {
    acc: 'acc_personal', from: P.amazon, subject: 'Shipped: "Mechanical keyboard (75%), hot-swappable"', cat: 'finance', read: true,
    text: `Hello Alex,\n\nYour package is on its way and should arrive Wednesday.\n\nTrack your package: https://www.amazon.de/progress-tracker/package/ref=ppx_yo_dt\n\nOrder #302-1928374-1122334`,
  },
  {
    acc: 'acc_personal', from: P.bank, subject: 'Your May statement is ready', cat: 'finance', folder: 'label:receipts',
    text: `Hi Alex,\n\nYour monthly statement for May is ready. You can download it in the app or find it attached.\n\nTotal spending: €2,184.40\nSavings goal progress: 64%\n\nTeam bunq`,
    att: [{ filename: 'statement-2024-05.pdf', mimeType: 'application/pdf', size: 221_000 }],
  },
  {
    acc: 'acc_personal', from: P.verge, subject: 'The Verge Weekly: foldables finally make sense', cat: 'newsletters',
    html: newsletterHtml('Foldables finally make sense', [
      ['The best foldable of the year is the one you can forget is a foldable', 'After a month with the latest generation, the crease is basically gone and the battery lasts all day.'],
      ['Why every app suddenly has an AI button', 'Product teams are racing to ship assistants. Some of them are even useful.'],
      ['Our favorite budget headphones', 'Great sound does not have to cost more than a nice dinner.'],
    ], '#5200ff'),
  },
  {
    acc: 'acc_personal', from: P.tldr, subject: 'TLDR: WebGPU lands everywhere, SQLite turns 24, and more', cat: 'newsletters', read: true,
    html: newsletterHtml('WebGPU lands everywhere', [
      ['WebGPU ships in all major browsers', 'With Safari joining, compute shaders in the browser are now a realistic target for production apps.'],
      ['SQLite at 24', 'A look back at the most deployed database engine in the world and what is next.'],
      ['The quiet rise of local-first software', 'CRDTs, sync engines and why your next app might work great offline.'],
    ], '#111827', false),
  },
  {
    acc: 'acc_personal', from: P.sec, subject: 'Security alert: new sign-in on Mac', cat: 'updates', read: true,
    html: `<div style="font-family:Roboto,Arial,sans-serif;max-width:480px;margin:auto;border:1px solid #dadce0;border-radius:8px;padding:24px;text-align:center"><div style="font-size:20px;color:#202124">A new sign-in on Mac</div><div style="color:#5f6368;margin:8px 0 16px">alex.morgan@gmail.com</div><p style="color:#3c4043;font-size:14px">We noticed a new sign-in to your Google Account on a Mac device. If this was you, you don't need to do anything.</p><a href="https://myaccount.google.com/notifications" style="display:inline-block;background:#1a73e8;color:#fff;padding:10px 20px;border-radius:4px;text-decoration:none">Check activity</a></div>`,
  },
  {
    acc: 'acc_personal', from: P.leo, subject: 'Book recommendations (as promised)', cat: 'social', read: true,
    text: `Hey Alex,\n\nHere's the list I mentioned at dinner:\n\n- The Overstory — Richard Powers\n- Piranesi — Susanna Clarke\n- Project Hail Mary — Andy Weir\n- The Design of Everyday Things — Don Norman (a reread for you, probably)\n\nLet me know what you pick!\nLeo`,
  },
  {
    acc: 'acc_personal', from: P.spotify, subject: 'Your Release Radar is ready', cat: 'newsletters', read: true,
    html: `<div style="background:#121212;color:#fff;font-family:Helvetica,Arial,sans-serif;padding:32px;text-align:center"><div style="font-size:28px;font-weight:800">Release Radar</div><p style="color:#b3b3b3">New music from artists you follow, every Friday.</p><img src="https://images.unsplash.com/photo-1511379938547-c1f69419868d?w=600" width="320" alt="" style="border-radius:8px"><p><a href="https://open.spotify.com" style="background:#1ed760;color:#000;padding:12px 28px;border-radius:999px;text-decoration:none;font-weight:700">Listen now</a></p></div>`,
  },
  {
    acc: 'acc_personal', from: P.dentist, subject: 'Appointment reminder — Tuesday 9:30', cat: 'updates',
    text: `Dear Alex Morgan,\n\nThis is a reminder of your dental check-up on Tuesday at 9:30.\nPlease reply to this email if you need to reschedule.\n\nSmile Dental Clinic\nPrinsengracht 221, Amsterdam`,
  },
  {
    acc: 'acc_personal', from: P.airbnb, subject: 'Reminder: leave a review for your stay in Porto', cat: 'travel', read: true,
    text: `Hi Alex, thanks for staying with Marta in Porto! Reviews help hosts and guests. It only takes a minute: https://www.airbnb.com/reviews`,
  },
  {
    acc: 'acc_personal', from: P.hn, subject: 'Hacker Newsletter #712', cat: 'newsletters', read: true,
    html: newsletterHtml('Hacker Newsletter #712', [
      ['Writing a toy database in Go', 'A step-by-step build of an LSM tree with a write-ahead log.'],
      ['How we cut our CI time by 70%', 'Remote caching, test sharding and being ruthless about flaky tests.'],
    ], '#ff6600', false),
  },
  // ---- side project (Gmail) ----
  {
    acc: 'acc_side', from: P.stripe, subject: 'Your receipt from Pixelforge Studio #1043-2291', cat: 'finance', folder: 'label:invoices',
    html: receiptHtml('Pixelforge Studio', [['Brand identity package', '€ 1,800.00'], ['Icon set (48 icons)', '€ 420.00'], ['VAT 21%', '€ 466.20']], '€ 2,686.20'),
    att: [{ filename: 'Invoice-1043-2291.pdf', mimeType: 'application/pdf', size: 98_000 }],
  },
  {
    acc: 'acc_side', from: P.rachel, subject: 'Logo concepts — round two', cat: 'work', starred: true, folder: 'label:customers',
    text: `Hi!\n\nThanks for the first round, the team loved concept B. For round two could we try:\n\n• a slightly heavier wordmark\n• the teal from concept A instead of the navy\n• a version that works as a 16px favicon\n\nNo rush — end of next week is perfect.\n\nRachel\nLumen Labs`,
    replies: [[me.acc_side, `Hi Rachel, great feedback. I'll have round two to you by Thursday next week.`], [P.rachel, `Perfect, thank you!`]],
  },
  {
    acc: 'acc_side', from: P.tomas, subject: 'Collaboration on the Rivera rebrand?', cat: 'work',
    text: `Hola,\n\nI run a small design studio in Mexico City. We just landed a rebrand for a restaurant group and need help with illustration and motion. Would Pixelforge be open to a 6-week collaboration starting in July?\n\nPortfolio: https://riveradesign.mx/work\n\nSaludos,\nTomás`,
  },
  {
    acc: 'acc_side', from: P.figma, subject: 'Rachel Kim commented on "Lumen — Logo R2"', cat: 'updates',
    html: `<div style="font-family:Inter,-apple-system,sans-serif;font-size:14px"><p><strong>Rachel Kim</strong> commented on <a href="https://figma.com/file/abc">Lumen — Logo R2</a></p><blockquote style="background:#f5f5f5;border-radius:6px;margin:0;padding:10px 14px">The favicon version is 🔥. Can we see it on a dark tab bar too?</blockquote></div>`,
  },
  {
    acc: 'acc_side', from: P.vercel, subject: 'Deployment ready: pixelforge-site (production)', cat: 'updates', read: true,
    text: `Your deployment of pixelforge-site to production is ready.\n\nhttps://pixelforge.studio\nCommit: 9f2c1ab "Add case study: Lumen Labs"`,
  },
  {
    acc: 'acc_side', from: P.notion, subject: 'What\'s new in Notion: calendar, forms and more', cat: 'newsletters', read: true,
    html: newsletterHtml('Calendar, forms and more', [
      ['Notion Calendar now syncs with Outlook', 'Bring every calendar into one place.'],
      ['Forms are here', 'Collect responses straight into a database.'],
    ], '#000000'),
  },
]


// Generated notification-style mail so the list is long enough for infinite scroll.
const GH_REPOS = ['northwind/atlas-mobile', 'northwind/atlas-api', 'alexm/dotfiles', 'pixelforge/site']
const GH_EVENTS = [
  ['Fix race in offline queue replay', 'merged'], ['Bump vite from 6.2 to 7.0', 'opened'], ['Add retry jitter to sync client', 'review requested'],
  ['Refactor settings screen', 'commented'], ['Flaky test: SyncEngine reconnect', 'opened'], ['Upgrade to Go 1.23', 'merged'],
  ['Dark mode polish for list rows', 'review requested'], ['Remove deprecated v1 endpoints', 'commented'], ['Speed up cold start by 30%', 'merged'],
  ['Add keyboard shortcuts help dialog', 'opened'], ['Pin GitHub Actions to SHAs', 'merged'], ['Crash when attachment name has emoji', 'opened'],
]
GH_EVENTS.forEach(([title, ev], i) => {
  const repo = GH_REPOS[i % GH_REPOS.length]
  const n = 380 + i * 7
  T.push({
    acc: repo.startsWith('pixelforge') ? 'acc_side' : repo.startsWith('alexm') ? 'acc_personal' : 'acc_work',
    from: P.github, subject: `[${repo}] ${title} (#${n})`, cat: 'updates', read: i % 3 !== 0,
    html: `<div style="font-family:-apple-system,Segoe UI,sans-serif;font-size:14px;color:#1f2328"><p><strong>@${['priyar', 'jweber', 'mlindqvist', 'dependabot'][i % 4]}</strong> ${ev} <a href="https://github.com/${repo}/pull/${n}">#${n}</a> in <strong>${repo}</strong>.</p><p style="color:#59636e;font-size:12px">— Reply to this email directly or <a href="https://github.com/${repo}/pull/${n}">view it on GitHub</a>.</p></div>`,
  })
})

const RECEIPTS: [string, string, string][] = [
  ['Apple', 'iCloud+ 200 GB', '€ 2.99'], ['Spotify', 'Premium Duo', '€ 14.99'], ['NS', 'Train ticket Amsterdam → Utrecht', '€ 8.80'],
  ['Albert Heijn', 'Groceries', '€ 63.12'], ['Uber', 'Trip on Friday evening', '€ 17.40'], ['GitHub', 'Copilot Pro (monthly)', '$ 10.00'],
  ['Figma', 'Professional seat', '$ 15.00'], ['Hetzner', 'Cloud server CX22', '€ 4.51'], ['Thuisbezorgd', 'Order from Pho Saigon', '€ 28.50'],
  ['Coolblue', 'USB-C hub', '€ 49.99'],
]
RECEIPTS.forEach(([merchant, item, total], i) => {
  T.push({
    acc: i % 3 === 2 ? 'acc_side' : 'acc_personal', from: { name: merchant, email: `receipts@${merchant.toLowerCase().replace(/[^a-z]/g, '')}.com` },
    subject: `Your receipt from ${merchant}`, cat: 'finance', read: i > 1, folder: i % 2 ? 'label:receipts' : undefined,
    html: receiptHtml(merchant, [[item, total]], total),
  })
})

const STANDUPS = ['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday']
STANDUPS.forEach((day, i) => {
  T.push({
    acc: 'acc_work', from: [P.priya, P.jonas, P.mara][i % 3], subject: `Async standup notes — ${day}`, cat: 'work', read: true,
    text: `Yesterday: ${['finished the sync conflict UI', 'paired on ATL-482', 'reviewed hiring loop feedback', 'wrote the migration checklist', 'demoed offline mode to Brightlane'][i]}\nToday: ${['QA pass on offline queue', 'roadmap deck polish', 'customer calls', 'release notes', 'retro + planning'][i]}\nBlockers: none`,
  })
})

const SPAM: Tpl[] = [
  { acc: 'acc_personal', from: { name: 'Prize Department', email: 'winner@lucky-prizes.biz' }, subject: 'You have WON an iPhone 15!!! Claim now', folder: 'spam', text: 'Click here to claim your prize: http://lucky-prizes.biz/claim' },
  { acc: 'acc_work', from: { name: 'IT Support', email: 'it-support@n0rthwind-security.com' }, subject: 'Urgent: password expires today', folder: 'spam', text: 'Your password expires today. Verify at http://n0rthwind-security.com/login' },
]
T.push(...SPAM)

const SENT: Tpl[] = [
  { acc: 'acc_work', from: me.acc_work, to: [P.client], subject: 'Re: Contract renewal & pricing for 2025', folder: 'sent', read: true, text: 'Hi Daniel,\n\nThanks — great to hear. I will send the updated pricing by Wednesday. SSO is included in the business tier.\n\nBest,\nAlex' },
  { acc: 'acc_personal', from: me.acc_personal, to: [P.leo], subject: 'Re: Book recommendations (as promised)', folder: 'sent', read: true, text: 'Starting with Piranesi. Thanks Leo!' },
  { acc: 'acc_side', from: me.acc_side, to: [P.tomas], subject: 'Re: Collaboration on the Rivera rebrand?', folder: 'sent', read: true, text: 'Hi Tomás, sounds exciting! Let\'s talk next week — does Tuesday 17:00 CET work?' },
]
T.push(...SENT)

T.push({ acc: 'acc_work', from: me.acc_work, to: [P.priya], subject: 'Draft: notes for leadership sync', folder: 'drafts', read: true, text: 'Points to raise:\n- staffing for offline sync\n- reporting templates scope' })
T.push({ acc: 'acc_personal', from: P.verge, subject: 'Archived: your subscription is confirmed', folder: 'archive', read: true, cat: 'newsletters', text: 'Thanks for subscribing to The Verge newsletters.' })
T.push({ acc: 'acc_personal', from: { name: 'Old Promo', email: 'promo@shop.example' }, subject: 'Last chance: 40% off everything', folder: 'trash', read: true, cat: 'newsletters', text: 'Sale ends tonight.' })

/** Incoming templates used by SyncNow to simulate new mail. */
export const INCOMING: Tpl[] = [
  { acc: 'acc_work', from: P.priya, subject: 'Quick question about the pricing deck', cat: 'work', text: 'Hey Alex, do you have the latest numbers for the EU region? I want to add them to slide 7 before the call.' },
  { acc: 'acc_personal', from: P.sam, subject: 'Pizza tonight?', cat: 'social', text: 'Napoli place at 7? Leo is in too.' },
  { acc: 'acc_side', from: P.rachel, subject: 'Round two looks amazing', cat: 'work', text: 'Hi! We picked option 2 with the teal. Can you prepare the final files?' },
  { acc: 'acc_personal', from: P.amazon, subject: 'Delivered: your package', cat: 'finance', text: 'Your package was delivered to the front door.' },
]

function hash(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619) >>> 0
  return h
}

function snippetOf(text: string): string {
  return text.replace(/\s+/g, ' ').trim().slice(0, 160)
}

function stripHtml(html: string): string {
  return html.replace(/<style[\s\S]*?<\/style>|<script[\s\S]*?<\/script>/gi, '').replace(/<[^>]+>/g, ' ').replace(/&nbsp;/g, ' ').replace(/&amp;/g, '&').replace(/&gt;/g, '>').replace(/&lt;/g, '<').replace(/\s+/g, ' ').trim()
}

let seq = 0
export function buildMessage(t: Tpl, date: number, threadId?: string, overrides: Partial<Message> = {}): Message {
  seq += 1
  const id = `msg_${seq.toString(36)}_${hash(t.subject + date).toString(36).slice(0, 5)}`
  const folder = t.folder ?? 'inbox'
  const folderIds = [`${t.acc}:${folder}`]
  if (folder.startsWith('label:')) folderIds.unshift(`${t.acc}:inbox`)
  const text = t.text ?? stripHtml(t.html ?? '')
  const atts: Attachment[] = (t.att ?? []).map((a, i) => ({ ...a, id: `${id}_att${i}`, inline: false, contentId: '' }))
  return {
    id, accountId: t.acc, remoteId: id.toUpperCase(), threadId: threadId ?? `thr_${id}`, folderIds,
    from: t.from, to: t.to ?? [me[t.acc]], subject: t.subject, snippet: snippetOf(text), date: new Date(date).toISOString(),
    unread: !t.read, starred: !!t.starred, hasAttachments: atts.length > 0,
    labels: [...(t.cat ? [t.cat] : []), ...(t.more ?? [])],
    labelDetails: [...(t.cat ? [t.cat] : []), ...(t.more ?? [])].map((labelId, i) =>
      hash(id + labelId) % 3 !== 0
        ? { labelId, source: 'ai', confidence: Math.round((0.62 + (hash(id + 'c' + i) % 37) / 100) * 100) / 100 }
        : { labelId, source: 'manual', confidence: 1 }),
    cc: t.cc ?? [], bcc: [], replyTo: [], bodyHtml: t.html ?? '', bodyText: t.text ?? '', attachments: atts,
    messageId: `<${id}@mock.gomail>`, inReplyTo: '', references: '', hasRemoteContent: /src=["']https?:/i.test(t.html ?? ''),
    ...overrides,
  }
}

export function buildSeedMessages(): Message[] {
  const out: Message[] = []
  // Spread templates over the last ~35 days, newest first, with deterministic jitter.
  T.forEach((t, i) => {
    const age = i < 6 ? i * 1.3 * HOUR + (hash(t.subject) % 40) * 60_000 : (i * 9.5 + (hash(t.subject) % 9)) * HOUR
    const date = now - age - 5 * 60_000
    if (t.replies?.length) {
      const first = buildMessage({ ...t, read: true }, date - (t.replies.length + 1) * 2.5 * HOUR)
      const threadId = first.threadId
      out.push(first)
      t.replies.forEach(([from, text], j) => {
        const last = j === t.replies!.length - 1
        const subject = t.subject.startsWith('Re:') ? t.subject : `Re: ${t.subject}`
        out.push(buildMessage({ ...t, from, text, html: undefined, subject, att: undefined, read: last ? t.read : true, starred: last ? t.starred : false, to: [t.from] },
          last ? date : date - (t.replies!.length - j) * 2.5 * HOUR, threadId, { inReplyTo: first.messageId }))
      })
      // Only the newest message keeps the label folder; older ones live in the thread.
    } else {
      out.push(buildMessage(t, date))
    }
  })
  return out
}

export function buildWelcome(acc: Account): Message[] {
  return [
    buildMessage({
      acc: acc.id, from: { name: acc.provider === 'gmail' ? 'Gmail Team' : 'Outlook Team', email: acc.provider === 'gmail' ? 'mail-noreply@google.com' : 'no-reply@microsoft.com' },
      subject: `Welcome to your new ${acc.provider === 'gmail' ? 'Gmail' : 'Outlook'} account`, cat: 'updates',
      text: 'This mailbox is now connected to gomail. New messages will appear here automatically.',
    }, Date.now() - 60_000),
  ]
}

export function asTemplateAccount(t: Tpl, accountIds: string[]): Tpl {
  return accountIds.includes(t.acc) ? t : { ...t, acc: accountIds[0] }
}

export const MOCK_MODELS: AIModel[] = [
  ['openai/gpt-4o-mini', 'OpenAI: GPT-4o-mini', 128000, 0.15, 0.6],
  ['openai/gpt-4.1', 'OpenAI: GPT-4.1', 1047576, 2, 8],
  ['openai/gpt-4.1-mini', 'OpenAI: GPT-4.1 Mini', 1047576, 0.4, 1.6],
  ['openai/gpt-4.1-nano', 'OpenAI: GPT-4.1 Nano', 1047576, 0.1, 0.4],
  ['openai/o4-mini', 'OpenAI: o4 Mini', 200000, 1.1, 4.4],
  ['anthropic/claude-sonnet-4', 'Anthropic: Claude Sonnet 4', 200000, 3, 15],
  ['anthropic/claude-3.5-haiku', 'Anthropic: Claude 3.5 Haiku', 200000, 0.8, 4],
  ['anthropic/claude-opus-4', 'Anthropic: Claude Opus 4', 200000, 15, 75],
  ['google/gemini-2.5-flash', 'Google: Gemini 2.5 Flash', 1048576, 0.3, 2.5],
  ['google/gemini-2.5-pro', 'Google: Gemini 2.5 Pro', 1048576, 1.25, 10],
  ['google/gemini-2.0-flash-lite-001', 'Google: Gemini 2.0 Flash Lite', 1048576, 0.075, 0.3],
  ['meta-llama/llama-3.3-70b-instruct', 'Meta: Llama 3.3 70B Instruct', 131072, 0.13, 0.4],
  ['meta-llama/llama-3.1-8b-instruct', 'Meta: Llama 3.1 8B Instruct', 131072, 0.02, 0.03],
  ['meta-llama/llama-3.3-8b-instruct:free', 'Meta: Llama 3.3 8B Instruct (free)', 128000, 0, 0],
  ['mistralai/mistral-small-3.2-24b-instruct', 'Mistral: Mistral Small 3.2 24B', 131072, 0.05, 0.1],
  ['mistralai/mistral-medium-3', 'Mistral: Mistral Medium 3', 131072, 0.4, 2],
  ['mistralai/mistral-nemo', 'Mistral: Mistral Nemo', 131072, 0.01, 0.02],
  ['deepseek/deepseek-chat-v3-0324', 'DeepSeek: DeepSeek V3 0324', 163840, 0.25, 0.85],
  ['deepseek/deepseek-r1', 'DeepSeek: R1', 163840, 0.4, 2],
  ['deepseek/deepseek-chat-v3-0324:free', 'DeepSeek: DeepSeek V3 0324 (free)', 163840, 0, 0],
  ['qwen/qwen3-235b-a22b', 'Qwen: Qwen3 235B A22B', 40960, 0.13, 0.6],
  ['qwen/qwen3-30b-a3b', 'Qwen: Qwen3 30B A3B', 40960, 0.08, 0.29],
  ['qwen/qwen-2.5-72b-instruct', 'Qwen2.5 72B Instruct', 32768, 0.12, 0.39],
  ['x-ai/grok-3-mini', 'xAI: Grok 3 Mini', 131072, 0.3, 0.5],
  ['x-ai/grok-4', 'xAI: Grok 4', 256000, 3, 15],
  ['cohere/command-r-plus-08-2024', 'Cohere: Command R+ (08-2024)', 128000, 2.5, 10],
  ['microsoft/phi-4', 'Microsoft: Phi 4', 16384, 0.07, 0.14],
  ['nousresearch/hermes-3-llama-3.1-70b', 'Nous: Hermes 3 70B Instruct', 131072, 0.1, 0.28],
  ['amazon/nova-lite-v1', 'Amazon: Nova Lite 1.0', 300000, 0.06, 0.24],
  ['amazon/nova-pro-v1', 'Amazon: Nova Pro 1.0', 300000, 0.8, 3.2],
  ['moonshotai/kimi-k2', 'MoonshotAI: Kimi K2', 131072, 0.14, 2.49],
  ['z-ai/glm-4.5', 'Z.AI: GLM 4.5', 131072, 0.2, 0.2],
  ['perplexity/sonar', 'Perplexity: Sonar', 127072, 1, 1],
  ['inception/mercury', 'Inception: Mercury', 128000, 0.25, 1],
].map(([id, name, ctx, p, c]) => ({ id: id as string, name: name as string, description: '', contextLength: ctx as number, promptPrice: p as number, completionPrice: c as number }))
