// Phishing warning on Junk: the checks rarely misfire, and the warning gates images and links.
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, reactive } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import MessageBody from '@/components/mail/MessageBody.vue'
import { checkPhishing, malformedDomain, randomName, siteOf } from '@/lib/phishing'
import type { Message } from '@/lib/types'
import { linkPrompt, followMailLink, cancelLink } from '@/composables/useLinkPrompt'
import { useSettingsStore } from '@/stores/settings'

const junk = (email: string, name = '', extra: { bodyHtml?: string; labels?: string[] } = {}) => ({ from: { name, email }, labels: ['junk'], ...extra })

describe('phishing checks', () => {
  it('only checks mail in Junk', () => {
    const scam = { from: { name: 'Prize', email: 'a@90lluqjtuph21rvyrn6vr68xryfh.com' }, labels: [] }
    expect(checkPhishing(scam).suspicious).toBe(false)
    expect(checkPhishing({ ...scam, labels: ['junk'] }).suspicious).toBe(true)
  })

  it('catches the tricks real phishing uses', () => {
    const cases: [string, string][] = [
      ['ZS3M5XDU@90LLUQJTUPH21RVYRN6VR68XRYFH.com', ''], // random-character domain
      ['info@-----mail.rF1KQq5hRsJe.com', ''], // a label can't start with "-"
      ['ZNURAWAG@---IFGXVGWASNN61HEN9ENQHHXZZFKZ.com', ''],
      ['news@𝗔𝗻𝘄𝗯-netherlands-client-23233978.nl', ''], // styled look-alike letters
      ['promo@Promo.nl-winner-44525420707440563145804907967509637429', ''], // no such ending
      ['info@offers.example', 'Your​ council pays​'], // hidden characters in the name
    ]
    for (const [email, name] of cases) expect(checkPhishing(junk(email, name)), email).toMatchObject({ suspicious: true })
    // Your own username at someone else's domain.
    expect(checkPhishing(junk('sam.rivers42@otbu.example.info'), 'sam.rivers42@hotmail.com').reasons[0]).toMatch(/your own username/)
    // Links to a bare IP address.
    expect(checkPhishing(junk('billing@example.com', 'Billing', { bodyHtml: '<a href="http://185.12.4.9/login">Pay</a>' })).suspicious).toBe(true)
  })

  it('leaves ordinary (even unwanted) mail alone', () => {
    const fine = [
      'noreply@mail.instagram.com', 'bonuskaart@nieuwsbrief.ah.nl', 'news@updates.ubisoft.com', 'security@facebookmail.com',
      'info@nucash.nl', 'newsletter@mkt.theforkmanager.com', 'hello@email.stability.ai', 'bounce-8f3a9c2e1b7d@em1234.mailgun.org',
      'x@123formbuilder.com', 'a@mail.example.co.uk', 'info@bücher.de', 'noreply@e.battle.net',
    ]
    for (const email of fine) expect(checkPhishing(junk(email, 'Newsletter 🎉 – deals')).suspicious, email).toBe(false)
    // Your own address elsewhere, and your other mailboxes, are not the "own username" trick.
    expect(checkPhishing(junk('sam.rivers42@outlook.com'), 'sam.rivers42@hotmail.com', ['sam.rivers42@outlook.com']).suspicious).toBe(false)
    expect(checkPhishing(junk('sam@other.example'), 'sam@hotmail.com').suspicious).toBe(false) // too short to mean anything
    // Emoji with joiners in a name are fine; a link to a private address in text isn't from HTML.
    expect(checkPhishing(junk('a@example.com', 'Family 👨‍👩‍👧')).suspicious).toBe(false)
  })

  it('helpers', () => {
    expect(siteOf('mail.news.example.co.uk')).toBe('example.co.uk')
    expect(siteOf('a.b.example.com')).toBe('example.com')
    expect(randomName('06oufyev8msogxtuqhalll5y93uv')).toBe(true)
    expect(randomName('stayconnected2024now')).toBe(false)
    expect(malformedDomain('example.com')).toBe(false)
    expect(malformedDomain('ex_ample.com')).toBe(true)
  })
})

const scamHtml = '<p>Claim your prize</p><img src="https://cdn.example/a.png" width="300" height="200"><a href="https://trusted.example/x">Open</a>'
const msg = (over: Partial<Message>) =>
  ({ id: 'm1', accountId: 'a', threadId: 't1', folderIds: [], from: { name: 'Prize', email: 'win@90lluqjtuph21rvyrn6vr68xryfh.com' }, to: [], cc: [], bcc: [], replyTo: [], subject: 's', snippet: '', date: '2026-01-01T00:00:00Z', unread: false, starred: false, hasAttachments: false, labels: ['junk'], labelDetails: [], bodyText: '', bodyHtml: scamHtml, attachments: [], hasRemoteContent: true, remoteContentBlocked: false, ...over }) as unknown as Message

describe('phishing warning', () => {
  afterEach(() => cancelLink())

  function mount(allowRemote: boolean, over: Partial<Message> = {}) {
    const pinia = createPinia()
    setActivePinia(pinia)
    const props = reactive({ message: msg(over), allowRemote })
    const el = document.createElement('div')
    document.body.appendChild(el)
    const host = createApp({ render: () => h(MessageBody, { message: props.message, allowRemote: props.allowRemote }) })
    host.use(pinia)
    host.mount(el)
    return { el, frame: () => el.querySelector('iframe')!, unmount: () => host.unmount() }
  }

  it('replaces the images banner and keeps images off until dismissed', async () => {
    const m = mount(true) // e.g. "always load images" is on
    expect(m.el.querySelector('[data-testid="phishing-warning"]')).not.toBeNull()
    expect(m.el.textContent).not.toContain('Load images')
    expect(m.frame().getAttribute('srcdoc')).toContain('img-src data: cid:;')

    ;[...m.el.querySelectorAll('button')].find((b) => b.textContent?.includes('I trust this message'))!.click()
    await nextTick()
    expect(m.el.querySelector('[data-testid="phishing-warning"]')).toBeNull()
    expect(m.frame().getAttribute('srcdoc')).toContain('img-src data: cid: https: http:;')
    m.unmount()
  })

  it('shows the images banner after dismissing when images are off', async () => {
    const m = mount(false, { remoteContentBlocked: true }) // the backend stripped the images
    ;[...m.el.querySelectorAll('button')].find((b) => b.textContent?.includes('I trust this message'))!.click()
    await nextTick()
    expect(m.el.textContent).toContain('Load images')
    m.unmount()
  })

  it('no warning for ordinary junk', () => {
    const m = mount(false, { from: { name: 'Shop', email: 'news@shop.example' }, remoteContentBlocked: true })
    expect(m.el.querySelector('[data-testid="phishing-warning"]')).toBeNull()
    expect(m.el.textContent).toContain('Load images')
    m.unmount()
  })

  it('links in phishing mail always ask, without "Always allow"', async () => {
    setActivePinia(createPinia())
    const settings = useSettingsStore()
    settings.settings = { trustedLinkDomains: ['trusted.example'] } as never
    followMailLink('https://trusted.example/x', 'Open', true)
    expect(linkPrompt.value).toMatchObject({ host: 'trusted.example', phishing: true })
  })
})
