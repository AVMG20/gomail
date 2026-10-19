# Releasing gomail

Releases are built by `.github/workflows/release.yml` on Linux, macOS and Windows. It
attaches these files to the GitHub release:

| Platform | Files |
|---|---|
| Linux | `.tar.gz`, `.AppImage`, `.deb`, `.rpm` |
| macOS | universal `.zip` and `.dmg` |
| Windows | portable `.exe` and the setup installer |

## Cutting a release

1. Make sure `main` is green in CI: `go vet`, `go test` and `go build` on all three
   OSes, plus the frontend build.
2. Pick a version following semver, e.g. `v1.4.0`. The tag without the `v` becomes
   `main.version`, which the About screen shows (`GetAppInfo`). The workflow also writes
   it into the platform metadata (Info.plist, Windows version info, nfpm).
3. Create the tag and release in either of these ways:
   ```sh
   git tag -a v1.4.0 -m "gomail 1.4.0"
   git push origin v1.4.0
   gh release create v1.4.0 --generate-notes        # publishing triggers the workflow
   ```
   Or draft the release in the GitHub UI and click **Publish**.
4. To rebuild an existing release, go to **Actions → Release → Run workflow** and enter
   the tag. The assets are re-uploaded, replacing the old ones (`--clobber`).
5. Smoke-test the artifacts. `GOMAIL_DEMO=1 ./gomail` runs without any credentials.

## Repository secrets

All of these are optional. Without them, users enter their own client IDs in **Settings
→ Accounts**.

| Secret | Baked in as |
|---|---|
| `GOMAIL_GOOGLE_CLIENT_ID` | `internal/auth.DefaultGoogleClientID` |
| `GOMAIL_GOOGLE_CLIENT_SECRET` | `internal/auth.DefaultGoogleClientSecret` |
| `GOMAIL_MICROSOFT_CLIENT_ID` | `internal/auth.DefaultMicrosoftClientID` |

When credentials conflict, the first of these that is set wins: Settings, then
environment variables (`GOMAIL_*`), then the baked-in default. Code signing and
notarization aren't set up yet; see the TODO at the end.

## OAuth clients for distribution

### Google (Gmail)

1. Create a dedicated Google Cloud project for the distributed app, enable the
   **Gmail API**, and create an OAuth client of type **Desktop app**. The steps are in
   `docs/BACKEND.md`.
2. The consent screen must use the **External** user type, with your app name, logo,
   support email, homepage and privacy policy URL. Verify that you own the domains you
   list.
3. **Scopes.** gomail requests `openid email profile` and
   `https://www.googleapis.com/auth/gmail.modify`:
   - `gmail.modify` is a **restricted scope**.
   - In "Testing" mode only listed test users (up to 100) can sign in, and their
     refresh tokens expire after 7 days.
   - To publish, submit the app for **verification**. That covers a scope justification,
     a demo video of the OAuth flow and the features that use Gmail, and a privacy
     policy that complies with the Google API Services User Data Policy, including the
     Limited Use requirements.
   - Google may also require an annual **third-party security assessment** (CASA) for
     restricted scopes. gomail stores mail locally, which helps, but the review is still
     required.
   - Plan for several weeks. Until verification completes, users see an "unverified app"
     warning.
   - gomail deliberately doesn't request the broader `https://mail.google.com/` scope.
     The only feature that would need it is permanently emptying Gmail Trash.
4. A Desktop client secret isn't confidential: Google's installed-app flow assumes it
   ships with the binary, and PKCE protects the flow. Still keep it in repository
   secrets rather than in the code.

### Microsoft (Outlook / Microsoft 365)

1. Register a **multitenant + personal accounts** app ("Accounts in any organizational
   directory and personal Microsoft accounts"):
   - Use platform **Mobile and desktop applications** with redirect `http://localhost`.
   - Turn on **Allow public client flows**.
   - Add the delegated Graph permissions `offline_access openid email profile User.Read
     Mail.ReadWrite Mail.Send MailboxSettings.Read`.
2. **Publisher verification:** link the app registration to a verified Microsoft Partner
   Network (Cloud Partner Program) account under **Branding & properties → Publisher
   verification**. Without it:
   - Users in other organizations see an "unverified" consent screen.
   - Many tenants block user consent to unverified multitenant apps entirely.
3. Some organizations require **admin consent** for `Mail.ReadWrite`. Their admins can
   grant it tenant-wide from the consent URL or in Enterprise applications.
4. No client secret is used; gomail is a public client with PKCE.

## Desktop integration files

`build/linux/gomail.desktop` registers gomail as a `mailto:` handler
(`MimeType=x-scheme-handler/mailto;`, `Exec=gomail %u`, category
`Network;Email;`). The Linux build regenerates the file in
`build/linux/Taskfile.yml` (`generate:dotdesktop`), so the task has to pass
the same values or each build overwrites it:

```yaml
  generate:dotdesktop:
    cmds:
      - mkdir -p {{.ROOT_DIR}}/build/linux/appimage
      - wails3 generate .desktop -name "{{.APP_NAME}}" -exec "{{.EXEC}}" -icon "{{.ICON}}" -outputfile "{{.ROOT_DIR}}/build/linux/{{.APP_NAME}}.desktop" -categories "{{.CATEGORIES}}" -genericname "Mail Client" -comment "Fast desktop mail client for Gmail and Outlook" -mimetype "x-scheme-handler/mailto;" -keywords "mail;email;gmail;outlook;"
    vars:
      EXEC: '{{.APP_NAME}} %u'
      CATEGORIES: 'Network;Email;'
```

The generator has no `StartupWMClass` flag, so a generated file lacks that
line; it is optional.

`build/darwin/Info.plist` declares the `mailto` URL scheme
(`CFBundleURLTypes`, rank `Alternate`). The release workflow runs
`wails3 update build-assets`, which merges its keys into the existing plist,
so this entry survives. That step also leaves `gomail.desktop` untouched.

## Pre-release checklist

- [ ] CI green on all platforms; `go test -race ./...` passes locally.
- [ ] `wails3 generate bindings -clean=true -ts -i` leaves no diff.
- [ ] Database migrations are append-only. Never edit a migration that has shipped; add a
      new one.
- [ ] `docs/BACKEND.md` matches the contract (methods, settings, events).
- [ ] Demo mode works, and adding a real Gmail and a real Outlook account works with the
      baked-in clients.
- [ ] `build/linux/gomail.desktop` still has the `mailto` MimeType after the Linux build.
- [ ] The update check sees the new tag: run `CheckForUpdate` from an older build, or
      check `https://api.github.com/repos/AVMG20/gomail/releases/latest`. Drafts and
      pre-releases are not returned by that endpoint.

## TODO

- macOS signing and notarization: an Apple Developer ID certificate plus `notarytool`
  secrets.
- Windows Authenticode signing.
- Auto-update: the app only checks GitHub and links to the release page; it doesn't
  download or install anything.
- Windows `mailto:` registration. The NSIS installer should write the Default Programs
  keys:
  - `HKCU\Software\Clients\Mail\gomail\Capabilities\URLAssociations` with
    `mailto` = `gomail.mailto`;
  - `HKCU\Software\Classes\gomail.mailto\shell\open\command` with
    `"<install dir>\gomail.exe" "%1"`;
  - `HKCU\Software\RegisteredApplications` with `gomail` =
    `Software\Clients\Mail\gomail\Capabilities`.

  The uninstaller removes them. Users then pick gomail under Settings → Default apps.
  The app already handles a `mailto:` argument.
