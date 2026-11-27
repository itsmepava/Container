# Eimsky

Eimsky pools apparel factories' LCL raw-material imports into full containers. Forwarders compete with sealed bids to carry each box, and each factory pays its share plus a small fee.

| Who | App | What they do |
|---|---|---|
| Factories (shippers) | Web: `/f` | Book space, see the space left and a capped cost estimate, upload documents, follow progress, download invoices, answer move requests, manage their team |
| Eimsky ops and admins | Web: `/ops`, `/admin` | Lane pools board, own-box/co-load/hybrid decision, load planner (CBM container visualizer: lane and sailing picker, bookings, container options per lane, loading and money, to-scale 3D boxes, convert to FCL, roll, loading rules), tenders and awards with audited overrides, bookings, invoices, lanes and sailings, company approvals, users, settings, audit log |
| Forwarders | Mobile (iOS and Android) | Register with documents and lanes, get push notifications for new tenders, place sealed bids, see awards with shipper contacts and documents, upload booking confirmations |
| JAAF / FAAMA | Web: `/insights` | Anonymised totals: volume, fill, all-in cost, cargo mix, by lane and HS code. Figures from fewer than 3 factories are hidden |

The web app uses the Eimsky brand (charcoal, green and lime; Bricolage Grotesque and DM Sans) with a sidebar workspace and light, dark and automatic themes: use the sun/moon button in the top bar or the account menu.

## Quickest start (Linux or macOS, nothing to install)

```bash
git clone -b claude/practical-knuth-j3ag78 https://github.com/itsmepava/Container.git eimsky
```

```bash
cd eimsky && ./start.sh
```

Open http://localhost:8080. The first run downloads Go and PostgreSQL into the project folder, which takes a few minutes. It doesn't need sudo or a system database. Run it as your normal user, stop it with Ctrl+C, and use `./start.sh --reset` for fresh demo data or `./start.sh --port 9000` for another port.

## Quick start (local, with your own Postgres)

You need Go 1.24+ (it fetches the 1.26 toolchain automatically) and PostgreSQL 14+.

```bash
createuser -s eimsky && createdb -O eimsky eimsky   # once
```

```bash
make seed    # migrate, then load the demo dataset (dated relative to today)
```

```bash
make run     # http://localhost:8080
```

Or with Docker:

```bash
make up      # Postgres + app on :8080, demo data loaded
```

### Demo accounts

Web accounts use the password `eimsky-demo`:

| Email | Role |
|---|---|
| `factory@eimsky.test` | Factory admin (Ceylon Weave Mills) |
| `staff@eimsky.test` | Factory staff |
| `ops@eimsky.test` | Eimsky ops |
| `admin@eimsky.test` | Eimsky admin |
| `jaaf@eimsky.test` | Association viewer |

Forwarders sign in on the mobile app with their phone number. In dev mode the one-time code appears on screen.

| Phone | Forwarder |
|---|---|
| `0770000001` | Blue Harbour Logistics (approved, has won boxes) |
| `0770000002` | Serendib Freight (approved) |
| `0770000009` | New Wave Forwarders (waiting for approval) |
| any new number | starts a fresh registration |

The demo includes:
- a Shanghai pool with closed bids, ready to award (reproduces the MVP numbers: USD 2,350 → 40.52 per W/M, factory pays USD 320.21)
- an open Ningbo tender
- a Shanghai week with 10 requests and one overflow lot, for the load planner
- an under-filled Chennai pool
- four weeks of awarded and invoiced history, for the insights dashboard

## Forwarder mobile app

```bash
cd mobile && npm install
```

```bash
EXPO_PUBLIC_API_URL=http://<your-computer-LAN-IP>:8080 npx expo start
```

Open it with a development build (`npx expo run:ios` / `npx expo run:android`, or `eas build --profile development`). Expo Go also works for everything except push notifications. `npx expo start --web` runs it in a browser.

## How it fits together

```
cmd/eimsky          single binary: serve | migrate | seed [--reset] | create-admin EMAIL PASSWORD
internal/domain     pure business rules: CBM and W/M, loading rules, first-fit planning,
                    container options, break-even, bid ranking, bill splitting (fully unit tested)
internal/app        services shared by web, API and jobs: auth (sessions, links, OTP),
                    bookings and holds, planner, moves, decisions, tenders and awards,
                    documents, invoices (PDF), notifications, insights, audit, jobs, seed
internal/db         Postgres pool and embedded SQL migrations
internal/files      AES-256-GCM encrypted document storage
internal/web        server-rendered web app (html/template + htmx, no JS framework)
internal/api        JSON API for the mobile app (/api/v1, bearer tokens)
mobile/             Expo Router app (TypeScript) for forwarders
e2e/                Playwright end-to-end test of the full lifecycle
```

**Lifecycle:**
1. A factory books (15-minute hold, then confirm). The lot is placed first-fit in booking order.
2. Ops checks the pool and decides own box, co-load or hybrid. They choose the vessel, move overflow lots (with the shipper's consent if wanted), then lock the plan.
3. Locking opens a tender for each box, invites forwarders on that lane and pushes a notification.
4. Bids are sealed. When bidding closes (on time, or when ops closes it early), ops awards:
   - The lowest compliant bid on the requested vessel is the automatic pick.
   - Any other choice is an override. It needs a written reason, is logged, and is blocked if it would push any shipper above its cost cap.
5. Each shipper's cost is its W/M share of the bid plus the fee per W/M, never above the estimate shown at booking.
6. The winner gets shipper contacts and documents (secure links) and uploads its booking confirmation.
7. Background jobs issue invoices once documents are complete. They also expire holds, close tenders, send reminders, deliver email and push, and delete shipment documents a set number of days after the vessel sails.

All business numbers can be edited in **Admin → Settings** and **Admin → Lanes**:
- fee per W/M
- stowage factor
- hold time
- co-load rates
- estimate caps
- expected bids per container type
- categories that need an import licence
- document retention
- the small-group hiding threshold

## Configuration

Set these as environment variables (see `.env.example`).

| Variable | Default | Notes |
|---|---|---|
| `DATABASE_URL` | local `eimsky` database | Postgres connection string |
| `BASE_URL` | `http://localhost:8080` | Used in emails and document links |
| `DEV_MODE` | `true` | Shows OTP codes, demo logins and `/dev/mail`. **Set `false` in production** |
| `FILE_KEY` | dev key | 64 hex chars (`openssl rand -hex 32`). Required when `DEV_MODE=false` |
| `DATA_DIR` | `./data` | Encrypted documents |
| `SMTP_*` | unset | Without SMTP, emails are logged and shown on `/dev/mail` |
| `SMS_WEBHOOK_URL`, `SMS_WEBHOOK_TOKEN` | unset | Eimsky POSTs `{"to","message"}` for OTP texts. Point it at your SMS provider (or a small adapter) |
| `EXPO_ACCESS_TOKEN`, `PUSH_ENABLED` | unset, `true` | Push goes through Expo's push service |
| `JOBS_INTERVAL` | `30s` | Background job tick |

## Tests

```bash
make test-go      # unit + Postgres integration tests (domain, services, web pages, API)
```

```bash
make test-mobile  # TypeScript check + Jest
```

```bash
make e2e          # browser + API: the full lifecycle, then every button and form for every role
```

CI (`.github/workflows/ci.yml`) runs all three on every push.

## Going live checklist

These need your accounts or decisions; everything else is built.

1. **Hosting:**
   - Run the Docker image (or the binary) behind HTTPS with managed Postgres.
   - Set `DEV_MODE=false`, `BASE_URL` and `FILE_KEY`, and back up the key.
   - Mount `DATA_DIR` on persistent storage.
2. **First admin:** `eimsky create-admin you@company.com 'a-strong-password'`. Then add lanes, sailings and staff from Admin.
3. **Email:** SMTP credentials from any provider (SES, Postmark, SendGrid, Mailgun).
4. **SMS:** an SMS provider for forwarder sign-in codes, reached through `SMS_WEBHOOK_URL`.
5. **Mobile app:**
   - Create an Expo account and run `eas init` in `mobile/` to get the `projectId` that push needs.
   - Set the production API URL in `mobile/eas.json`.
   - Add an Apple Developer account and a Google Play Console account.
   - Upload the FCM credentials to EAS for Android push.
   - Then `eas build` and `eas submit`.
6. **Content:**
   - real lanes and co-load rates
   - vessel schedules
   - forwarder invitations
   - the app icon and splash (`mobile/assets`)
