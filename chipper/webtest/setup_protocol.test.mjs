// Unit tests for the robot setup protocol modules in webroot/setup/rts (Digital Dream Labs' vector-web-setup,
// MIT, kept as native ES modules). Run: node --test chipper/webtest/
import { test } from "node:test";
import assert from "node:assert/strict";

const rts = new URL("../webroot/setup/rts/", import.meta.url);
const load = (name) => import(new URL(name, rts));

test("every protocol module loads as an ES module", async () => {
  const modules = {
    "bleMessageProtocol.js": "BleMessageProtocol",
    "blesh.js": "Blesh",
    "clad.js": "Clad",
    "messageExternalComms.js": "Anki",
    "rtsCliUtil.js": "RtsCliUtil",
    "rtsV2Handler.js": "RtsV2Handler",
    "rtsV3Handler.js": "RtsV3Handler",
    "rtsV4Handler.js": "RtsV4Handler",
    "rtsV5Handler.js": "RtsV5Handler",
    "rtsV6Handler.js": "RtsV6Handler",
    "settings.js": "Settings",
    "stack.js": "Stack",
    "vectorBluetooth.js": "VectorBluetooth",
  };
  for (const [file, name] of Object.entries(modules)) {
    const mod = await load(file);
    assert.ok(["function", "object"].includes(typeof mod[name]) && mod[name] !== null, `${file} exports ${name}`);
  }
});

test("a short message goes out as one solo frame", async () => {
  const { BleMessageProtocol } = await load("bleMessageProtocol.js");
  const ble = new BleMessageProtocol(20);
  const frames = [];
  ble.onSendRaw((frame) => frames.push(frame));
  ble.sendMessage([1, 2, 3]);
  assert.deepEqual(frames, [[(0b11 << 6) | 3, 1, 2, 3]]);
});

test("a long message is split into frames and put back together", async () => {
  const { BleMessageProtocol } = await load("bleMessageProtocol.js");
  const sender = new BleMessageProtocol(20);
  const receiver = new BleMessageProtocol(20);
  const message = Array.from({ length: 50 }, (_, i) => i);
  const received = [];
  receiver.setDelegate({ handleReceive: (buffer) => received.push(buffer) });
  sender.onSendRaw((frame) => {
    assert.ok(frame.length <= 20, `frame of ${frame.length} bytes`);
    receiver.receiveRawBuffer(frame);
  });
  sender.sendMessage(message);
  assert.deepEqual(received, [message]);
});

test("a Wi-Fi connect request survives pack and unpack", async () => {
  const { Anki } = await load("messageExternalComms.js");
  const { RtsWifiConnectRequest } = Anki.Vector.ExternalComms;
  const sent = new RtsWifiConnectRequest("4d79576946", "secret pass", 15, 6, false);
  const received = new RtsWifiConnectRequest();
  received.unpack(sent.pack());
  assert.deepEqual(
    { ssid: received.wifiSsidHex, pw: received.password, timeout: received.timeout, auth: received.authType, hidden: received.hidden },
    { ssid: "4d79576946", pw: "secret pass", timeout: 15, auth: 6, hidden: false }
  );
});
