# Robot setup over Bluetooth

The page that pairs a robot over Web Bluetooth, sets its Wi-Fi and links it to this server, served by wire-pod at
`http://localhost:8080/setup/` (Home → Robot setup links to it). Web Bluetooth needs Chrome (or another Chromium
browser) and a secure context; `localhost` is one, `http://<LAN address>:8080/setup/` is not, so open it in Chrome
on the PC that runs wire-pod. Without Web Bluetooth the page says so instead of starting.

Everything it loads comes from this server; there is no build step (the browser loads the ES modules directly).

## How it works

It is Digital Dream Labs' [vector-web-setup](https://github.com/digital-dream-labs/vector-web-setup) at `0637cf7`
(MIT, see `LICENSE`) with its Node server and the accounts cloud replaced by two wire-pod endpoints
(`chipper/pkg/wirepod/config-ws/setup.go`):

| Step | vector-web-setup | Here |
|------|------------------|------|
| Stack | chosen from `default-data/settings.json` (accounts endpoint, API key) | one built-in `wire-pod` stack, selected automatically |
| Pair, PIN, Wi-Fi | Web Bluetooth + `rts/` | unchanged |
| OTA (only robots in recovery mode, RTS v2) | `POST /firmware` lists the server's firmware folder | `GET /api/setup/otas` → `{"base_url": "http://<LAN IP>:<port>/api/get_ota/", "files": [...]}`, the `.ota` files in `<data dir>/robot/ota`. The robot downloads the file itself over Wi-Fi, so the URL uses the PC's LAN address, not localhost |
| Account | email/password sign-in, `POST <accounts>/1/sessions` | no form: `GET /api/setup/sessions` returns wire-pod's fixed local session token, which the page hands the robot (`doAnkiAuth`); the robot then authenticates against wire-pod |
| Settings | Alexa opt-in, analytics opt-in, time zone, units | time zone and units; the Alexa call is gone (our firmware has no Alexa) and data collection is sent as off |

## What was adapted

- `index.html` is `templates/main.ejs` without EJS: the server IP and port it injected are not needed (the OTA base
  URL comes from `/api/setup/otas`). Removed: Google Fonts (system fonts instead), the stack chooser, the sign-in and
  sign-up forms, the Alexa and analytics checkboxes, the Bootstrap JS bundle (nothing used it), the DDL, Vector and
  Chrome logos.
- Look: the page is on the v3ctor design system (`/css/style.css`, `/js/shell.js`, `/assets/icons.svg`) like the rest
  of the UI: shared top bar, light and dark themes, a step indicator (Pair, PIN, Wi-Fi, Update only for robots in
  recovery mode, Link, Settings), one card per step, a robot card and the terminal in a collapsed "Advanced: terminal"
  panel. DDL's dark theme (`vecSetup.css`), Bootstrap CSS and the Font Awesome icons are gone; `css/setup.css` holds
  what the page adds. The element ids and the `vec-*` classes `app.js` drives are DDL's, so the flow is unchanged.
- `app.js` is `rts-js/main.js` as an ES module (strict): `_build` is declared, the jQuery/pterm globals it uses are
  listed, and `js/sodium.js` is loaded only after `window.sodium.onload` is set (with `async` it could run first).
  Fixed on the way: the OTA rows had a stray quote that dropped their `data-value`, so choosing one of several OTAs
  threw; the locale came from `navigator.locale`, which browsers do not have, and is now `navigator.language`.
  UI-only changes for the new look: the Wi-Fi and OTA rows use our icons and signal bars, progress bars set their fill
  width, a PIN step and a "Back to the list" button on the Wi-Fi password card were added, `toggleIcon(id, false)`
  now clears a step (DDL marked it done either way), and DDL's three views (Alt+1/2/3) open or close the terminal
  panel instead of resizing side-by-side panes.
- `css/pterm.css` is `less/pterm.less` compiled by hand (`@size: 8px`) and restyled with the design system's tokens.
- `js/pterm.js` reads keys and pastes only while the terminal panel is open and nothing else has focus (DDL's read
  every key on the page, so space and arrows could not scroll and a pasted Wi-Fi password also landed in it).
- `rts/`: the setup protocol (BLE framing, CLAD messages, RTS v2-v6 handlers), unchanged except that CommonJS
  `require`/`module.exports` became `import`/`export`. Known upstream bug kept: `doConnectionId()` in the v3-v6
  handlers reads an undeclared `url` and throws if called; nothing calls it.

## Bundled files and licenses

| File | Origin | License |
|------|--------|---------|
| `index.html`, `app.js`, `rts/`, `css/pterm.css`, `js/pterm.js`, `images/*.svg` (the onboarding drawings, painted in the accent color through a CSS mask) | vector-web-setup `0637cf7` | MIT, Digital Dream Labs (`LICENSE`) |
| `css/setup.css` | v3ctor | same as wire-pod |
| `js/jquery.min.3.3.1.js` | jQuery 3.3.1, as bundled by vector-web-setup | MIT (header in the file) |
| `js/sodium.js` | libsodium.js, as bundled by vector-web-setup | ISC (`js/sodium.LICENSE`) |

## Tests

- `node --test chipper/webtest/setup_protocol.test.mjs`: protocol modules (module graph, BLE framing, CLAD).
- `chipper/webtest/test_ui.py`: the page loads from this server only, its modules resolve, and without Web
  Bluetooth it shows the Chrome message; Robot setup links here; it uses the shared top bar and stylesheet, follows
  the dark theme, shows the pairing card and steps with (faked) Web Bluetooth, keeps the terminal collapsed and deaf
  until opened, and fits a 390 px phone.
- `go test ./pkg/wirepod/config-ws/` (from `chipper/`): `/api/setup/sessions` and `/api/setup/otas`.
- Pairing a real robot is manual: Chrome on this PC, `http://localhost:8080/setup/`.
