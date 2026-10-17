# Robot setup over Bluetooth

The page that pairs a robot over Web Bluetooth, sets its Wi-Fi and links it to this server, served by wire-pod at
`http://localhost:8080/setup/` (Web Bluetooth needs a secure context; `localhost` is one).

- `rts/`: the robot's setup protocol (BLE framing, CLAD messages, RTS v2-v6 handlers) from Digital Dream Labs'
  [vector-web-setup](https://github.com/digital-dream-labs/vector-web-setup) at `0637cf7`, MIT (see `LICENSE`).
  The files are unchanged except that CommonJS `require`/`module.exports` became ES module `import`/`export`, so the
  browser loads them directly: there is no build step.
- Known upstream bug kept as is: `doConnectionId()` in the v3-v6 handlers reads an undeclared `url` and throws if
  called; nothing calls it.
- Tests: `node --test chipper/webtest/setup_protocol.test.mjs`.
