/* Copyright (c) 2019-2020 Digital Dream Labs. See LICENSE file for details. */

// The setup page logic: vector-web-setup's rts-js/main.js (0637cf7) as an ES module, adapted to run against this
// wire-pod server only. What changed is listed in README.md; the BLE flow and the protocol calls are DDL's.

// jQuery (js/jquery.min.3.3.1.js) and the terminal (js/pterm.js) are classic scripts loaded before this module;
// these are the globals they define that this module uses.
/* global $, newLine, pterm_changeprompt, pterm_insert_history, pterm_new_progress_bar, pterm_on, pterm_print,
   pterm_read, pterm_set, pterm_set_progress_bar */
/* global pterm_env:writable, pterm_handled:writable */

import { VectorBluetooth } from "./rts/vectorBluetooth.js";
import { RtsCliUtil } from "./rts/rtsCliUtil.js";
import { IntBuffer } from "./rts/clad.js";
import { RtsV2Handler } from "./rts/rtsV2Handler.js";
import { RtsV3Handler } from "./rts/rtsV3Handler.js";
import { RtsV4Handler } from "./rts/rtsV4Handler.js";
import { RtsV5Handler } from "./rts/rtsV5Handler.js";
import { RtsV6Handler } from "./rts/rtsV6Handler.js";
import { Sessions } from "./rts/sessions.js";
import { Settings } from "./rts/settings.js";
import { TYPE } from "./rts/stack.js";

// One built-in stack instead of DDL's default-data/settings.json: the robot talks to this wire-pod server, so
// there are no account endpoints or API keys.
const STACK_NAME = "wire-pod";
const WIRE_POD_SETTINGS = { stacks: { [STACK_NAME]: {} } };

// wire-pod endpoints that replace vector-web-setup's Express server and the accounts cloud.
const SESSION_URL = "/api/setup/sessions";
const OTAS_URL = "/api/setup/otas";

let rtsHandler = null;
let vecBle = new VectorBluetooth();

let _sodium = null;

let selectedNetwork = null;
let scannedNetworks = [];
let cloudSession = {};
let _version = 0;
let _wifiCredentials = null;
let _settings = null;
let _stack = null;
let _otaEndpoint = null;
let _build = "prod";
let urlParams = {};
let _statusInterval = null;
let _enableAutoFlow = true;
let _sessions = new Sessions();
let _filter = null;
let _cmdPending = false;
setView(_sessions.getViewMode(), false);

window.sodium = {
  onload: function (sodium) {
    //
    _sodium = sodium;
  },
};
// sodium.js calls window.sodium.onload once it is ready, so it is loaded only after the hook above exists.
const sodiumScript = document.createElement("script");
sodiumScript.src = "js/sodium.js";
sodiumScript.async = true;
document.body.appendChild(sodiumScript);

// set build type
function parseParams() {
  let params = new URL(window.location.href).searchParams;
  let buildType = params.get("build");
  switch (buildType) {
    case "dev":
    case "beta":
    case "prod":
      _build = buildType;
      break;
    default:
      _build = "prod";
      break;
  }

  let wifiSsid = params.get("wifiSsid");
  let wifiPassword = params.get("wifiPassword");

  if (wifiSsid != null) {
    _wifiCredentials = {
      ssid: wifiSsid,
      pw: wifiPassword,
      auth: 6,
    };
  }
}

//************* Settings ******************
function handleStackSelection(stackName) {
  _stack = _settings.getStack(stackName);

  configurePtrem();

  $("#boxVectorEnv").removeClass("vec-hidden");
  $("#vecEnv").html(stackName);

  toggleIcon("iconEnv", true);

  setPhase("containerDiscover");
}

function configurePtrem() {
  // load env
  let env = _sessions.getEnv();
  if (env != null) {
    pterm_env = env;
  }

  pterm_on("env", function () {
    _sessions.setEnv(pterm_env);
    _sessions.save();
  });

  // pterm_set("OTA_LKG", _stack.otaEndpoints);
  pterm_set("OTA_URL", urlParams["otaUrl"]);

  let lastVector = _sessions.getLastVector();
  if (lastVector) {
    pterm_set("LAST", lastVector);
  }

  pterm_insert_history("ble-connect '" + lastVector + "'");
}

//**************** OTA *******************
// Only factory/recovery robots (RTS v2) take an OTA here; the robot downloads it from this server over Wi-Fi.
function setupOTAFiles() {
  if (_version != 2) {
    toggleIcon("iconOta", true);
    startAccountPhase();
    return;
  }

  if (_stack === null) {
    return;
  }

  getOtasPresent().then(
    (data) => {
      let files = Array.isArray(data.files) ? data.files : [];
      if (files.length == 0) {
        console.log("No otas found on this server");
      }

      var otaUrls = [];

      files.map((name) => {
        var obj = parseURL(data.base_url + encodeURIComponent(name));
        obj.type = TYPE.LOCAL;
        otaUrls.push(obj);
      });

      console.log(otaUrls);

      showOtaChoices(otaUrls);
    },
    () => showOtaChoices([])
  );
}

function showOtaChoices(otaUrls) {
  // The update step is shown only for the robots that need one.
  $("#iconOta").prop("hidden", false);
  setPhase("containerOta");

  // No URL present
  if (otaUrls.length == 0) {
    $("#containerOtaSelection").addClass("vec-hidden");
    $("#containerOtaNoImage").removeClass("vec-hidden");
  }
  // One URL present
  else if (otaUrls.length == 1) {
    $("#containerOtaSelection").addClass("vec-hidden");
    $("#otaUpdate").removeClass("vec-hidden");
    _otaEndpoint = otaUrls[0].href;
    doOta();
  }
  // Multiple URL's presents
  else {
    var urlViews = "";
    otaUrls.map((url, index) => (urlViews += generateOtaFileRow(index, url)));
    $("#otaSelection").html(urlViews);

    $(".vec-ota-row").click(function () {
      $("#containerOtaSelection").addClass("vec-hidden");
      $("#otaUpdate").removeClass("vec-hidden");

      var selectedUrl = $(this).data().value;
      _otaEndpoint = otaUrls[selectedUrl].href;

      pterm_set("OTA_LKG", _otaEndpoint);
      // Previous version allowed webclient to be configured using
      // url params. OTA_URL was used to send that to pterm
      pterm_set("OTA_URL", _otaEndpoint);

      doOta();
    });
  }
}

function parseURL(url) {
  var obj = new URL(url);
  obj.filename = decodeURIComponent(url.substring(url.lastIndexOf("/") + 1));
  return obj;
}

// The .ota files in <data dir>/robot/ota, with the LAN base URL the robot can download them from.
function getOtasPresent() {
  return new Promise((resolve, reject) => {
    $.getJSON(OTAS_URL)
      .done(function (data) {
        resolve(data);
      })
      .fail(function (data) {
        reject(data);
      });
  });
}

function generateOtaFileRow(value, urlObj) {
  let iconName = urlObj.type == TYPE.CLOUD ? "download" : "cube";

  // DDL had a stray quote before data-value, which left the row without its value.
  return (
    `<div data-value="${value}" class="vec-ota-row pick-row" role="button" tabindex="0">` +
    icon(iconName) +
    `<div>` +
    `<div class="vec-ota-name">${urlObj.filename}</div>` +
    `<div class="vec-ota-host">${urlObj.hostname}</div>` +
    `</div>` +
    icon("chevron-down", "chev") +
    `</div>`
  );
}

function icon(name, extraClass = "") {
  return `<svg class="icon ${extraClass}" aria-hidden="true"><use href="/assets/icons.svg#${name}"></use></svg>`;
}

function getOtaUrl() {
  if ("otaUrl" in urlParams) {
    return urlParams["otaUrl"];
  } else {
    return _otaEndpoint;
  }
}

//**************** Wifi *******************

function connectToWifi(ssid, pw, auth) {
  rtsHandler.doWifiConnect(ssid, pw, auth, 15).then(function (msg) {
    if (msg.value.wifiState == 2 || msg.value.wifiState == 1) {
      $("#wifiConnectErrorLabel").addClass("vec-hidden");
      toggleIcon("iconWifi", true);
      setupOTAFiles();
    } else {
      // wifi failed
      $("#wifiConnectErrorLabel").removeClass("vec-hidden");
      scanForWifi();
      _wifiCredentials = null;
    }
  });
}

// Marks a step done (on) or not done (off, e.g. the pairing step after a disconnect).
function toggleIcon(iconId, on) {
  $("#" + iconId).removeClass("vec-icon-active");
  $("#" + iconId).toggleClass("vec-icon-done", on);
}

function setPhase(phase) {
  // general clearing
  $("#txtPin").val("");

  // update icon
  let icon = $("#" + phase).attr("icon");
  if (icon != null && icon != "") {
    $(".vec-icon").removeClass("vec-icon-active");
    $("#" + icon).addClass("vec-icon-active");
  }

  $(".vec-container.vec-current").css("opacity", 0);
  $(".vec-container.vec-current").removeClass("vec-current");
  $("#" + phase).addClass("vec-current");
  $("#" + phase).css("opacity", 1);
}

function setProgress(bar, percent) {
  let value = Math.round(Math.min(Math.max(percent, 0), 1) * 100);
  $(bar).attr("aria-valuenow", value).children(".vec-progress-bar-fill").css("width", value + "%");
}

function setOtaProgress(percent) {
  setProgress("#progressBarOta", percent);
}

function setLogProgress(percent) {
  setProgress("#progressBarLogs", percent);
}

function generateWifiRow(hex, auth, strength) {
  let ssid = RtsCliUtil.convertHexToStr(hex);
  let n = "1";
  if (strength > 45) {
    n = "2";
  }
  if (strength > 65) {
    n = "3";
  }
  return (
    `<div class="vec-wifi-row pick-row" role="button" tabindex="0" authType="${auth}" hexId="${hex}">` +
    `<span class="signal s${n}" title="Signal ${n}/3"><i></i><i></i><i></i></span>` +
    `<div class="vec-wifi-ssid">${ssid}</div>` +
    icon("chevron-down", "chev") +
    "</div>"
  );
}

function displayWifiNetworks(m) {
  let wifiHtml = "";

  for (let i = 0; i < m.scanResult.length; i++) {
    if (m.scanResult[i].wifiSsidHex == "hidden") continue;

    wifiHtml += generateWifiRow(
      m.scanResult[i].wifiSsidHex,
      m.scanResult[i].authType,
      m.scanResult[i].signalStrength
    );
  }

  $("#wifiScanTable").html(wifiHtml);
}

function handleDisconnected() {
  cleanRtsHandler();
  pterm_changeprompt("", null);
  clearInterval(_statusInterval);
  $("#boxVectorStatus").addClass("vec-hidden");
  toggleIcon("iconBle", false);
  setPhase("containerDiscover");
}

function doOta() {
  if (_version == 2) {
    rtsHandler.doOtaStart(getOtaUrl()).then(
      function (msg) {
        console.log("ota success");
      },
      function (msg) {
        console.log(msg);
        $("#otaErrorLabel").removeClass("vec-hidden");
        $("#btnTryAgain").removeClass("vec-hidden");
      }
    );
  } else {
    toggleIcon("iconOta", true);
    startAccountPhase();
  }
}

// DDL's three views (1: setup only, 2: setup and terminal, 3: terminal only, Alt+1/2/3) map to the collapsed or open
// "Advanced: terminal" panel; the setup cards always stay on the page.
function setView(mode, animate) {
  _sessions.setViewMode(mode);
  _sessions.save();
  showTerminal(mode == 2 || mode == 3, animate);
}

function showTerminal(open, scroll) {
  let panel = document.getElementById("terminalPanel");
  panel.open = open;
  if (open && scroll) {
    panel.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }
}

// Web Bluetooth exists only in Chromium browsers and only on a secure page (localhost or HTTPS): opened from
// another machine at http://<LAN address>/setup/ it is missing even in Chrome.
function showIncompatible() {
  let msg =
    "Vector setup needs Google Chrome (or another Chromium browser) with Web Bluetooth. Open this page in Chrome " +
    "on the PC that runs wire-pod.";
  if (!window.isSecureContext) {
    let port = window.location.port ? ":" + window.location.port : "";
    msg =
      "Vector setup needs Google Chrome with Web Bluetooth, which only works on a secure page. Open " +
      `http://localhost${port}/setup/ in Chrome on the PC that runs wire-pod.`;
  }
  $("#incompatibleMessage").text(msg);
  setPhase("containerIncompatible");
}

$(document).ready(function () {
  $(document).keydown(function (event) {
    if (event.altKey) {
      if (event.keyCode == 49) {
        setView(1, true);
      } else if (event.keyCode == 50) {
        setView(2, true);
      } else if (event.keyCode == 51) {
        setView(3, true);
      }
    }
  });

  console.log(
    " __      ________ _____ _______ ____  _____  \n" +
      " \\ \\    / /  ____/ ____|__   __/ __ \\|  __ \\ \n" +
      "  \\ \\  / /| |__ | |       | | | |  | | |__) |\n" +
      "   \\ \\/ / |  __|| |       | | | |  | |  _  / \n" +
      "    \\  /  | |___| |____   | | | |__| | | \\ \\ \n" +
      "     \\/   |______\\_____|  |_|  \\____/|_|  \\_\\"
  );

  console.log(
    "\nURL parameters:\n" +
      "\t    wifiSsid = WIFI_SSID\n" +
      "\twifiPassword = WIFI_PASSWORD\n\n"
  );

  if (!navigator.bluetooth) {
    showIncompatible();
    return;
  }

  // process url params
  parseParams();

  _settings = new Settings(WIRE_POD_SETTINGS);
  handleStackSelection(STACK_NAME);

  // listen to ble messages
  vecBle.onReceive(handleRtsHandshake);
  vecBle.onDisconnected(handleDisconnected);
  vecBle.onCancelSelect(function () {
    setPhase("containerDiscover");

    if (_cmdPending) {
      _cmdPending = false;
      newLine();
    }
  });

  $("#wifiScanTable").on("click", ".vec-wifi-row", function () {
    for (let i = 0; i < scannedNetworks.length; i++) {
      if (scannedNetworks[i].wifiSsidHex == $(this).attr("hexId")) {
        selectedNetwork = scannedNetworks[i];
      }
    }

    $("#txtWifiSsid").val($(this).children(".vec-wifi-ssid").html());
    $("#txtWifiSsid").addClass("readonly");
    $("#txtWifiSsid").prop("readonly", true);
    setPhase("containerWifiConfig");
  });

  $("#btnBackToWifi").click(function () {
    setPhase("containerWifi");
  });

  // Enter picks the focused row, or presses the card's first visible button (a focused button handles it itself).
  $(".vec-container").on("keypress", function (e) {
    if (e.which != 13 || $(e.target).is("button, a")) {
      return true;
    }
    let row = $(e.target).closest(".vec-wifi-row, .vec-ota-row");
    (row.length ? row : $(this).find(".actions button:visible").first()).trigger("click");
    return false;
  });

  pterm_on("cmd", function (args) {
    if (rtsHandler != null) {
      pterm_handled = rtsHandler.handleCli(args);
    } else {
      pterm_handled = true;

      if (args[0] == "help") {
        let helpArgs = {
          "ble-connect": {
            args: 0,
            des: "Scan and connect to a Vector",
            help: "ble-connect [VECTOR_NAME]",
          },
          "ble-clear": {
            args: 0,
            des: "Clear stored session data.",
            help: "ble-clear",
          },
          echo: {
            args: 1,
            des: "Echo text to terminal.",
            help: "echo $LAST",
          },
          export: {
            args: 1,
            des: "Save env variables.",
            help: "export MY_VAR=0 OTHER_VAR=40",
          },
          printenv: {
            args: 0,
            des: "Print environment variables.",
            help: "printenv",
          },
          unset: {
            args: 1,
            des: "Unset env variables",
            help: "unset MY_VAR",
          },
        };
        pterm_print(RtsCliUtil.printHelp(helpArgs));
      } else if (args[0] == "ble-connect") {
        if (args[1]) {
          if (args[1].length == 4) {
            _filter = "Vector " + args[1];
          } else {
            _filter = args[1];
          }
        } else {
          _filter = null;
        }
        $("#btnDiscoverVector").click();
        _cmdPending = true;
        pterm_handled = false;
      } else if (args[0] == "ble-clear") {
        _sessions.clearSessions();
        _sessions.save();
      }
    }
  });
});

let handleRtsHandshake = {};
handleRtsHandshake.receive = function (data) {
  if (data[0] == 1 && data.length == 5) {
    // This message is a handshake from Vector
    let version = IntBuffer.BufferToUInt32(data.slice(1));
    HandleHandshake(version);
  } else {
    // Received message after version handler exists
  }
};

// Replaces DDL's account sign-in (POST <accounts>/1/sessions): wire-pod answers with the fixed local session.
function getLocalSession() {
  return new Promise(function (resolve, reject) {
    $.getJSON(SESSION_URL)
      .done(function (data) {
        resolve(data);
      })
      .fail(function (data) {
        reject(data);
      });
  });
}

function GenerateHandshakeMessage(version) {
  let buffer = IntBuffer.Int32ToLE(version);
  return [1].concat(buffer);
}

function scanForWifi() {
  rtsHandler.doWifiScan().then(function (m) {
    scannedNetworks = m.value;
    displayWifiNetworks(m.value);
    setPhase("containerWifi");
  });
}

function updateStatusBox(m) {
  $("#boxVectorStatus").removeClass("vec-hidden");
  if (m.value.wifiState == 1 || m.value.wifiState == 2) {
    $("#vecInfoWifi").removeClass("vec-hidden");
    $("#vecStatusSsid").html(RtsCliUtil.convertHexToStr(m.value.wifiSsidHex));
  } else {
    $("#vecInfoWifi").addClass("vec-hidden");
  }
  $("#vecInfoEsn").html(m.value.esn);
  $("#vecInfoBuild").html(m.value.version.split("-")[0]);
  $("#vecStatusTitle").html(vecBle.bleName);

  if (
    _version == 2 ||
    _version == 3 ||
    !m.value.hasOwner ||
    m.value.isCloudAuthed
  ) {
    // RtsV2 or Cloud authorized

    // Save session
    saveSession();

    enableLogPanel();
  }
}

function saveSession() {
  // Save session
  let remoteKey = rtsHandler.remoteKeys.publicKey;
  let name = vecBle.bleName;
  let encryptKey = rtsHandler.cryptoKeys.encrypt;
  let decryptKey = rtsHandler.cryptoKeys.decrypt;
  _sessions.setSession(remoteKey, name, encryptKey, decryptKey);
  _sessions.setKeys(rtsHandler.keys.publicKey, rtsHandler.keys.privateKey);
  _sessions.save();
}

function enableLogPanel() {
  $("#boxStatusLogs").removeClass("vec-hidden");
}

function cleanRtsHandler() {
  if (rtsHandler != null) {
    rtsHandler.cleanup();
    rtsHandler = null;
  }
}

function HandleHandshake(version) {
  cleanRtsHandler();
  toggleIcon("iconBle", true);

  switch (version) {
    case 6:
      // RTSv6
      rtsHandler = new RtsV6Handler(vecBle, _sodium, _sessions);
      break;
    case 5:
      // RTSv5
      rtsHandler = new RtsV5Handler(vecBle, _sodium, _sessions);
      break;
    case 4:
      // RTSv4
      rtsHandler = new RtsV4Handler(vecBle, _sodium, _sessions);
      break;
    case 3:
      // RTSv3 (Dev)
      rtsHandler = new RtsV3Handler(vecBle, _sodium, _sessions);
      break;
    case 2:
      // RTSv2 (Factory)
      rtsHandler = new RtsV2Handler(vecBle, _sodium, _sessions);
      break;
    default:
      // Unknown
      break;
  }

  _version = version;

  rtsHandler.onReadyForPin(function () {
    setPhase("containerEnterPin");

    if (_cmdPending) {
      pterm_read("Enter pin:").then(function (args) {
        _enableAutoFlow = false;
        _cmdPending = false;
        rtsHandler.enterPin(args[0]);
        setPhase("containerLoading");
      });
    }
  });

  rtsHandler.onEncryptedConnection(function () {
    toggleIcon("iconPin", true);
    $("#discoverFirstTime").addClass("vec-hidden");
    $("#discoverReconnect").removeClass("vec-hidden");

    if (_statusInterval != null) {
      clearInterval(_statusInterval);
    }

    if (_cmdPending) {
      _cmdPending = false;
      newLine();
    }

    rtsHandler.doStatus().then(function (m) {
      updateStatusBox(m);

      if (!_enableAutoFlow) {
        // early out of auto update flow: the rest is done in the terminal
        showTerminal(true, true);
        return;
      }

      if (m.value.wifiState == 1 || m.value.wifiState == 2) {
        toggleIcon("iconWifi", true);
        // skip wifi scan
        setupOTAFiles();
      } else if (_wifiCredentials != null) {
        // try to reconnect with stored credentials
        rtsHandler.doWifiScan().then(function (m) {
          connectToWifi(
            _wifiCredentials.ssid,
            _wifiCredentials.pw,
            _wifiCredentials.auth
          );
        });
      } else {
        scanForWifi();
      }
    });

    _sessions.setLastVector(vecBle.bleName);
    _sessions.save();
    pterm_set("LAST", vecBle.bleName);

    pterm_changeprompt(
      "[v" + _version + "] " + vecBle.bleName.split(" ")[1],
      "blue"
    );
  });

  if (rtsHandler.onCloudAuthorized) {
    rtsHandler.onCloudAuthorized(function (value) {
      if (value.success) {
        // save session
        saveSession();
      }
    });
  }

  rtsHandler.onOtaProgress(function (value) {
    if (value.status == 2) {
      let progress = Number(value.current) / Number(value.expected);
      setOtaProgress(progress);
    } else if (value.status == 3) {
      // handle OTA complete
      toggleIcon("iconOta", true);
      setPhase("containerDiscover");
    } else {
      // todo: handle failure
    }
  });

  rtsHandler.onLogProgress(function (value) {
    let progress = Number(value.packetNumber) / Number(value.packetTotal);
    setLogProgress(progress);
  });

  rtsHandler.onCliResponse(function (output) {
    pterm_print(output);
    newLine();
  });

  rtsHandler.onPrint(function (output) {
    pterm_print(output);
  });

  rtsHandler.onCommandDone(function () {
    newLine();
  });

  rtsHandler.onNewProgressBar(function () {
    pterm_new_progress_bar();
  });

  rtsHandler.onUpdateProgressBar(function (value, total) {
    pterm_set_progress_bar(value, total);
  });

  rtsHandler.onLogsDownloaded(function (name, logFile) {
    var file = new Blob(logFile, { type: ".tar.gz" });
    var a = document.createElement("a"),
      url = URL.createObjectURL(file);
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    setTimeout(function () {
      document.body.removeChild(a);
      window.URL.revokeObjectURL(url);
    }, 0);
  });

  vecBle.send(GenerateHandshakeMessage(version));
}

$("#btnTryAgain").click(function () {
  $("#btnTryAgain").addClass("vec-hidden");
  $("#otaErrorLabel").addClass("vec-hidden");
  doOta();
});

$("#btnDiscoverVector").click(function () {
  setPhase("containerLoading");
  vecBle.tryConnect(_filter);
  _filter = null;
});

$("#btnEnterPin").click(function () {
  _enableAutoFlow = $("#checkboxEnableAutoFlow").is(":checked");
  rtsHandler.enterPin($("#txtPin").val());
  setPhase("containerLoading");
});

$("#btnConnectWifi").click(function () {
  let auth = 6;
  if (selectedNetwork != null) {
    auth = selectedNetwork.authType;
  }

  _wifiCredentials = {
    ssid: $("#txtWifiSsid").val(),
    pw: $("#txtWifiPw").val(),
    auth: auth,
  };

  setPhase("containerLoading");

  connectToWifi(_wifiCredentials.ssid, _wifiCredentials.pw, auth);

  $("#txtWifiSsid").val("");
  $("#txtWifiPw").val("");
});

$("#btnCustomWifi").click(function () {
  selectedNetwork = null;
  $("#txtWifiSsid").removeClass("readonly");
  $("#txtWifiSsid").prop("readonly", false);
  setPhase("containerWifiConfig");
});

// The account step: no sign-in form, the robot is linked to wire-pod with the local session right away.
function startAccountPhase() {
  setPhase("containerAccount");
  linkToWirePod();
}

$("#btnConnectCloud").click(function () {
  linkToWirePod();
});

function linkToWirePod() {
  $("#accountErrorLabel").addClass("vec-hidden");
  $("#btnConnectCloud").addClass("vec-hidden");
  setPhase("containerLoading");

  getLocalSession().then(
    function (data) {
      cloudSession.sesionToken = data.session.session_token;

      rtsHandler.doAnkiAuth(cloudSession.sesionToken).then(function (msg) {
        if (msg.value.success) {
          cloudSession.clientToken = msg.value.clientTokenGuid;

          // adjust default timezone
          let tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
          let jqtz = $("#selectTimeZone option[value='" + tz + "']");
          jqtz.prop("selected", "selected");

          toggleIcon("iconAccount", true);
          setPhase("containerSettings");
          enableLogPanel();
        } else {
          // todo: handle case when session token fails
          handleLoginError("Vector did not accept the wire-pod session. Check that wire-pod is running and try again.");
        }
      });
    },
    function (data) {
      console.log(data);
      handleLoginError("Could not get the session from wire-pod (" + SESSION_URL + "). Is wire-pod running?");
    }
  );
}

function handleLoginError(msg) {
  $("#accountErrorLabel").text(msg);
  $("#accountErrorLabel").removeClass("vec-hidden");
  $("#btnConnectCloud").removeClass("vec-hidden");
  setPhase("containerAccount");
}

// DDL first sent /v1/alexa_opt_in; our firmware has no Alexa, so that call is gone. The analytics opt-in went to
// DDL's cloud; here the account settings go to wire-pod and data collection stays off.
$("#btnFinishSetup").click(function () {
  let timezone = $("#selectTimeZone").val();
  // DDL read navigator.locale, which browsers do not have (it sent "undefined").
  let locale = navigator.language;
  let isFahrenheit = $("#selectTemperature").val() == "fahrenheit";
  let isMetric = $("#selectDistance").val() == "metric";
  let allowDataAnalytics = false;

  setPhase("containerLoading");

  rtsHandler
    .doSdk(
      cloudSession.clientToken,
      "1",
      "/v1/update_account_settings",
      '{ "account_settings":{ "data_collection":' +
        allowDataAnalytics +
        ', "app_locale":"' +
        locale +
        '"} }'
    )
    .then(function (response) {
      // todo: check response for status 200
      rtsHandler
        .doSdk(
          cloudSession.clientToken,
          "1",
          "/v1/update_settings",
          JSON.stringify({
            settings: {
              time_zone: timezone,
              locale: locale,
              dist_is_metric: isMetric,
              temp_is_fahrenheit: isFahrenheit,
            },
          })
        )
        .then(function (settingsResponse) {
          // todo: check response for status 200 then wake
          rtsHandler
            .doSdk(
              cloudSession.clientToken,
              "1",
              "/v1/send_onboarding_input",
              JSON.stringify({
                onboarding_mark_complete_and_exit: {},
              })
            )
            .then(function (msg) {
              toggleIcon("iconSettings", true);
              setPhase("containerComplete");
            });
        });
    });
});

$("#btnDownloadLogs").click(function () {
  $("#btnDownloadLogs").addClass("vec-hidden");
  $("#panelLogs").removeClass("vec-hidden");
  rtsHandler.doLog().then(function () {
    $("#btnDownloadLogs").removeClass("vec-hidden");
    $("#panelLogs").addClass("vec-hidden");
  });
});
