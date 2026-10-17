var colorPicker = new iro.ColorPicker("#picker", {
  width: 250,
  layout: [
    {
      component: iro.ui.Wheel,
    },
  ],
});

var stimRunning = false;

var urlParams = new URLSearchParams(window.location.search);
esn = urlParams.get("serial");

var client = new HttpClient();
getCurrentSettings();

function revealSdkActions() {
  var x = document.getElementById("sdkActions");
  x.style.display = "block";
}

// Draws the last stimulation values (0..1) as a line in the #stimChart SVG (viewBox 110 x 40).
function drawStim(values) {
  const line = document.querySelector("#stimChart .sparkline-line");
  if (!line) {
    return;
  }
  const step = values.length > 1 ? 110 / (values.length - 1) : 0;
  line.setAttribute(
    "points",
    values.map((v, i) => `${(i * step).toFixed(1)},${(40 - Math.max(0, Math.min(1, Number(v) || 0)) * 38).toFixed(1)}`).join(" ")
  );
}

function stimHandler() {
  // array to store data
  let stimData = [];

  // get the data every half a second
  interval = setInterval(() => {
    if (stimRunning == false) {
      sendForm("/api-sdk/stop_event_stream");
      clearInterval(interval);
    }
    fetch("/api-sdk/get_stim_status?serial=" + esn)
      .then((response) => response.json())
      .then((data) => {
        // add the data to the array
        stimData.push(data);

        // if the array has more than 12 datapoints, remove the first one
        if (stimData.length > 12) {
          stimData.shift();
        }

        drawStim(stimData);
      })
      .catch(() => {
        // robot unreachable: keep the last line
      });
  }, 500);
}

function showSection(id) {
  var headings = document.getElementsByClassName("toggleable-section");
  for (var i = 0; i < headings.length; i++) {
    headings[i].style.display = "none";
  }
  document.getElementById(id).style.display = "block";
  updateColor(id);
  if (id == "section-stim") {
    logDiv = document.getElementById("stimStatus");
    logP = document.createElement("p");
    stimRunning = true;
    sendForm("/api-sdk/begin_event_stream");
    stimHandler();
  } else {
    stimRunning = false;
  }
}

function sendForm(formURL) {
  let xhr = new XMLHttpRequest();
  if (formURL.includes("?")) {
    formURL = formURL + "&serial=" + esn;
  } else {
    formURL = formURL + "?serial=" + esn;
  }
  xhr.open("POST", formURL);
  xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
  xhr.send();
  xhr.onload = function () {
    getCurrentSettings();
  };
}

function getPhotos() {
  photoSection = document.getElementById("photoSection");
  let xhr = new XMLHttpRequest();
  xhr.open("POST", "/api-sdk/get_image_ids?serial=" + esn);
  xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
  xhr.send();
  xhr.onload = function () {
    photoSection.innerHTML = "";
    if (xhr.response == "null") {
      var noPhotos = document.createElement("p");
      noPhotos.className = "empty-state";
      noPhotos.innerHTML = "No photos yet. Ask the robot to take one, then refresh.";
      photoSection.appendChild(noPhotos);
      return;
    }
    try {
      imageIds = JSON.parse(xhr.response);
    } catch {
      var unreachable = document.createElement("p");
      unreachable.className = "empty-state";
      unreachable.textContent = "Can't read the photos: the robot is not answering.";
      photoSection.appendChild(unreachable);
      return;
    }
    for (let i = 0; i < imageIds.length; i++) {
      // let, not var: each Delete button must keep its own photo id.
      const imgId = imageIds[i];
      var thumb = document.createElement("div");
      thumb.className = "photo";
      var thumbLink = document.createElement("a");
      var thumbPic = document.createElement("img");
      var thumbDelete = document.createElement("button");
      thumbPic.src = "/api-sdk/get_image_thumb?serial=" + esn + "&id=" + imgId;
      //thumb.classList = "center"
      thumbPic.alt = "Photo " + imgId;
      thumbPic.loading = "lazy";
      thumbDelete.type = "button";
      thumbDelete.className = "secondary";
      thumbLink.target = "_blank";
      thumbLink.href = "/api-sdk/get_image?serial=" + esn + "&id=" + imgId;
      thumbLink.appendChild(thumbPic);
      thumbDelete.onclick = function () {
        deletePhoto(imgId);
      };
      thumbDelete.innerHTML = "Delete";
      thumb.appendChild(thumbLink);
      thumb.appendChild(thumbDelete);
      photoSection.appendChild(thumb);
    }
  };
}

function deletePhoto(id) {
  if (confirm("Are you sure?")) {
    // run code here
    let xhr = new XMLHttpRequest();
    xhr.open("POST", "/api-sdk/delete_image?serial=" + esn + "&id=" + id);
    xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
    xhr.send();
    xhr.onload = function () {
      getPhotos();
    };
  }
}

function goToControlPage() {
  window.location.href = "./control.html?serial=" + esn;
}

function sendLocation() {
  locationInput = document.getElementById("locationInput").value;
  if (locationInput == "") {
    alert("Location cannot be blank.");
    return;
  }
  let xhr = new XMLHttpRequest();
  xhr.open(
    "POST",
    "/api-sdk/location?serial=" + esn + "&location=" + locationInput
  );
  xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
  xhr.send();
  xhr.onload = function () {
    getCurrentSettings();
  };
}

function sendTimeZone() {
  timezone = document.getElementById("tzInput").value;
  if (timezone == "") {
    alert("Time zone cannot be blank.");
    return;
  }
  let xhr = new XMLHttpRequest();
  xhr.open("POST", "/api-sdk/timezone?serial=" + esn + "&timezone=" + timezone);
  xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
  xhr.send();
  xhr.onload = function () {
    getCurrentSettings();
  };
}

function sendCustomColor() {
  var pickerHue = colorPicker.color.hue;
  var pickerSat = colorPicker.color.saturation;
  var sendHue = pickerHue / 360;
  var sendHue = sendHue.toFixed(3);
  var sendSat = pickerSat / 100;
  var sendSat = sendSat.toFixed(3);
  let data = "hue=" + sendHue + "&" + "sat=" + sendSat;
  let xhr = new XMLHttpRequest();
  xhr.open("POST", "/api-sdk/custom_eye_color?serial=" + esn);
  xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
  xhr.send(data);
  xhr.onload = function () {
    getCurrentSettings();
  };
}

function updateStats() {
  let xhr = new XMLHttpRequest();
  xhr.open("POST", "/api-sdk/get_robot_stats?serial=" + esn);
  xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
  xhr.setRequestHeader("Cache-Control", "no-cache, no-store, max-age=0");
  xhr.responseType = "json";
  xhr.send();
  xhr.onload = function () {
    if (xhr.status !== 200 || !xhr.response) {
      return; // robot unreachable; the offline notice says so
    }
    var statsJson = JSON.stringify(xhr.response);
    var jdocthing = JSON.parse(statsJson);
    var jdocSecs = jdocthing["Alive.seconds"];
    var secondsInADay = 24 * 60 * 60; // Total seconds in a day
    var jdocDays = Math.round(jdocSecs / secondsInADay);
    var jdocTrigger = jdocthing["BStat.ReactedToTriggerWord"];
    var featuresUsed = jdocthing["FeatureType.Utility"];
    var petMs = jdocthing["Pet.ms"];
    var cmMoved = jdocthing["Stim.CumlPosDelta"];

    var s1 = document.getElementById("statsSection");
    s1.innerHTML = "";
    var list = document.createElement("dl");
    list.className = "kv";
    [
      ["Days alive", jdocDays],
      ["Woke up to its name", jdocTrigger + " times"],
      ["Utility features used", featuresUsed],
      ["Time petted", Math.round(petMs / 1000) + " s"],
      ["Distance driven", Math.round(cmMoved / 100) + " cm"],
    ].forEach(([name, value]) => {
      var dt = document.createElement("dt");
      dt.textContent = name;
      var dd = document.createElement("dd");
      dd.textContent = value;
      list.appendChild(dt);
      list.appendChild(dd);
    });
    s1.appendChild(list);
    //{
    //    "Alive.seconds" : 1619,
    //    "BStat.ReactedToTriggerWord" : 17,
    //    "FeatureType.Utility" : 2,
    //    "Pet.ms" : 3480,
    //    "Stim.CumlPosDelta" : 11148
    // }
  };
}

function getCurrentSettings() {
  updateStats();
  let xhr = new XMLHttpRequest();
  xhr.open("POST", "/api-sdk/get_sdk_settings?serial=" + esn);
  xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
  xhr.setRequestHeader("Cache-Control", "no-cache, no-store, max-age=0");
  xhr.responseType = "json";
  xhr.send();
  xhr.onload = function () {
    // An unreachable robot answers with an error text, which a "json" request turns into null.
    const offline = document.getElementById("robotOffline");
    if (xhr.status !== 200 || !xhr.response) {
      if (offline) offline.style.display = "";
      return;
    }
    if (offline) offline.style.display = "none";
    var jdocSdkSettingsResponse1 = JSON.stringify(xhr.response);
    jdocSdk = JSON.parse(jdocSdkSettingsResponse1);
    let xhr2 = new XMLHttpRequest();
    if (jdocSdk["custom_eye_color"]) {
      var customECE = jdocSdk["custom_eye_color"]["enabled"];
      var customECH = jdocSdk["custom_eye_color"]["hue"];
      var customECS = jdocSdk["custom_eye_color"]["saturation"];
    }
    eyeColorS = jdocSdk["eye_color"];
    var volumeS = jdocSdk["master_volume"];
    var localeS = jdocSdk["locale"];
    var timeSetS = jdocSdk["clock_24_hour"];
    var tempFormatS = jdocSdk["temp_is_fahrenheit"];
    var location = jdocSdk["default_location"];
    var timezone = jdocSdk["time_zone"];
    if (jdocSdk["custom_eye_color"]) {
      if (`${customECE}` == "true") {
        var setHue = customECH * 360;
        var setHue = setHue.toFixed(3);
        var setSat = customECS * 100;
        var setSat = setSat.toFixed(3);
        colorPicker.color.hsl = { h: setHue, s: setSat, l: 50 };
        var eyeColorT = "Custom";
      } else {
        if (`${eyeColorS}` == 0) {
          eyeColorT = "Teal";
        } else if (`${eyeColorS}` == 1) {
          eyeColorT = "Orange";
        } else if (`${eyeColorS}` == 2) {
          eyeColorT = "Yellow";
        } else if (`${eyeColorS}` == 3) {
          eyeColorT = "Lime Green";
        } else if (`${eyeColorS}` == 4) {
          eyeColorT = "Azure Blue";
        } else if (`${eyeColorS}` == 5) {
          eyeColorT = "Purple";
        } else if (`${eyeColorS}` == 6) {
          eyeColorT = "Other Green";
        } else {
          eyeColorT = "none";
        }
      }
    } else {
      if (`${eyeColorS}` == 0) {
        eyeColorT = "Teal";
      } else if (`${eyeColorS}` == 1) {
        eyeColorT = "Orange";
      } else if (`${eyeColorS}` == 2) {
        eyeColorT = "Yellow";
      } else if (`${eyeColorS}` == 3) {
        eyeColorT = "Lime Green";
      } else if (`${eyeColorS}` == 4) {
        eyeColorT = "Azure Blue";
      } else if (`${eyeColorS}` == 5) {
        eyeColorT = "Purple";
      } else if (`${eyeColorS}` == 6) {
        eyeColorT = "Other Green";
      } else {
        eyeColorT = "none";
      }
    }
    if (`${volumeS}` == 0) {
      var volumeT = "Mute";
    } else if (`${volumeS}` == 1) {
      var volumeT = "Low";
    } else if (`${volumeS}` == 2) {
      var volumeT = "Medium Low";
    } else if (`${volumeS}` == 3) {
      var volumeT = "Medium";
    } else if (`${volumeS}` == 4) {
      var volumeT = "Medium High";
    } else if (`${volumeS}` == 5) {
      var volumeT = "High";
    } else {
      var volumeT = "none";
    }
    if (`${timeSetS}` == "false") {
      var timeSetT = "12 Hour";
    } else {
      var timeSetT = "24 Hour";
    }
    if (`${tempFormatS}` == "true") {
      var tempFormatT = "Fahrenheit";
    } else {
      var tempFormatT = "Celsius";
    }

    var s1 = document.getElementById("currentVolume");
    const s1P = document.createElement("p");
    document.getElementById(volumeT).checked = true;

    var s2 = document.getElementById("currentEyeColor");
    const s2P = document.createElement("p");
    if (eyeColorT != "none" && eyeColorT != "Custom") {
      document.getElementById(eyeColorT).checked = true;
    }

    var s3 = document.getElementById("currentLocale");
    const s3P = document.createElement("p");
    document.getElementById(localeS).checked = true;

    var s4 = document.getElementById("currentTimeSet");
    const s4P = document.createElement("p");
    document.getElementById(timeSetT).checked = true;

    var s5 = document.getElementById("currentTempFormat");
    const s5P = document.createElement("p");
    document.getElementById(tempFormatT).checked = true;

    var s10 = document.getElementById("currentLocation");
    const s10P = document.createElement("p");
    s10P.textContent = "Current Location Setting: " + `${location}`;
    document.getElementById("locationInput").placeholder = `${location}`;
    s10.innerHTML = "";
    s10.appendChild(s10P);

    var s11 = document.getElementById("currentTimeZone");
    const s11P = document.createElement("p");
    s11P.textContent = "Current Time Zone Setting: " + `${timezone}`;
    document.getElementById("tzInput").value = `${timezone}`;
    s11.innerHTML = "";
    s11.appendChild(s11P);
  };
}

renderBatteryInfo(esn);