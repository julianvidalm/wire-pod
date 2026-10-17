"""Browser tests for the web UI (chipper/webroot), without the server or a robot.

The pages are served from webroot by a local static server; every /api, /api-sdk and /api-ble request is answered
by fixtures in this file, and the requests the pages send are recorded so the tests can check them.

Run: python -m venv .venv && .venv/bin/pip install playwright pytest && .venv/bin/playwright install chromium
     .venv/bin/pytest chipper/webtest
"""
import functools
import http.server
import json
import pathlib
import threading
import urllib.parse

import pytest
from playwright.sync_api import Page, expect, sync_playwright

WEBROOT = pathlib.Path(__file__).resolve().parent.parent / "webroot"

INTENTS = [
    {"name": "lights on", "description": "desk lights", "utterances": ["lights on", "turn on the lights"],
     "intent": "intent_greeting_hello", "params": {"paramname": "", "paramvalue": ""}, "exec": "/bin/true",
     "execargs": [""], "luascript": "", "issystem": False},
    {"name": "lights off", "description": "desk lights off", "utterances": ["lights off"],
     "intent": "intent_greeting_goodbye", "params": {"paramname": "", "paramvalue": ""}, "exec": "",
     "execargs": [""], "luascript": "", "issystem": False},
]

SDK_SETTINGS = {"master_volume": 3, "eye_color": 4, "locale": "en-US", "clock_24_hour": True,
                "temp_is_fahrenheit": False, "default_location": "Montreal, Quebec", "time_zone": "America/Toronto"}


@pytest.fixture(scope="session")
def server():
    handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(WEBROOT))
    httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    yield f"http://127.0.0.1:{httpd.server_address[1]}"
    httpd.shutdown()


@pytest.fixture(scope="session")
def browser():
    with sync_playwright() as p:
        b = p.chromium.launch()
        yield b
        b.close()


class Api:
    """Answers the pages' API calls and records them as (method, path, query, body)."""

    def __init__(self, page: Page, robot_online=True, photos=None, kg=None):
        self.calls = []
        self.robot_online = robot_online
        self.photos = photos or []
        self.kg = kg or {"provider": "", "key": ""}
        page.route("**/api*/**", self.handle)

    def handle(self, route):
        req = route.request
        url = urllib.parse.urlparse(req.url)
        self.calls.append((req.method, url.path, urllib.parse.parse_qs(url.query), req.post_data))
        status, body = self.answer(url.path)
        route.fulfill(status=status, body=body if isinstance(body, str) else json.dumps(body),
                      content_type="text/plain" if isinstance(body, str) else "application/json")

    def answer(self, path):
        robot = {
            "/api-sdk/get_sdk_info": {"robots": [{"esn": "0dd1b652", "ip_address": "192.168.68.52"}]},
            "/api-sdk/get_battery": {"battery_level": 3, "battery_volts": 4.0, "is_on_charger_platform": True},
            "/api-sdk/get_sdk_settings": SDK_SETTINGS,
            "/api-sdk/get_robot_stats": {"Alive.seconds": 172800, "BStat.ReactedToTriggerWord": 5,
                                         "FeatureType.Utility": 2, "Pet.ms": 3000, "Stim.CumlPosDelta": 1000},
            "/api-sdk/get_image_ids": self.photos,
            "/api-sdk/get_faces": "null",
            "/api-sdk/get_stim_status": 0.5,
        }
        if path in robot:
            return (200, robot[path]) if self.robot_online else (200, "error: robot unreachable")
        return {
            "/api/is_api_v3": (200, "it is!"),
            "/api/get_config": (200, {"pastinitialsetup": True}),
            "/api/get_custom_intents_json": (200, INTENTS),
            "/api/get_version_info": (200, {"commit": "abc1234", "version": ""}),
            "/api/get_kg_api": (200, self.kg),
            "/api/get_weather_api": (200, {"provider": "", "key": ""}),
            "/api/get_logs": (200, "log line"),
            "/api/get_stt_info": (200, {"provider": "whisper.cpp", "language": "en-US"}),
        }.get(path, (200, "success"))

    def sent(self, path):
        return [c for c in self.calls if c[1] == path]


@pytest.fixture
def page(browser):
    ctx = browser.new_context(viewport={"width": 1280, "height": 900})
    pg = ctx.new_page()
    pg.errors = []
    pg.on("pageerror", lambda e: pg.errors.append(str(e)))
    yield pg
    ctx.close()


def test_home_opens_custom_intents_without_starting_bluetooth(page, server):
    api = Api(page)
    page.goto(f"{server}/index.html")
    expect(page.locator("#section-intents")).to_be_visible()
    expect(page.locator(".tab.active")).to_have_text("Custom intents")
    page.wait_for_timeout(300)
    assert not api.sent("/api-ble/init")
    assert page.errors == []


def test_tab_from_the_url_hash_opens_directly(page, server):
    Api(page)
    page.goto(f"{server}/index.html#about")
    expect(page.locator("#cVersion")).to_have_text("Commit abc1234")
    expect(page.locator(".tab.active")).to_have_text("About")


def test_add_intent_sends_the_same_payload(page, server):
    api = Api(page)
    page.goto(f"{server}/index.html")
    page.fill("#nameAdd", "lamp")
    page.fill("#descriptionAdd", "desk lamp")
    page.fill("#utterancesAdd", "lamp on,light please")
    page.select_option("#intentAddSelectintents", "intent_clock_time")
    page.fill("#execAdd", "/usr/bin/lamp")
    page.fill("#execAddArgs", "on,full")
    page.click("text=Add intent")
    expect(page.locator(".toast")).to_contain_text("success")
    body = json.loads(api.sent("/api/add_custom_intent")[0][3])
    assert body == {"name": "lamp", "description": "desk lamp", "utterances": ["lamp on", "light please"],
                    "intent": "intent_clock_time", "params": {"paramname": "", "paramvalue": ""},
                    "exec": "/usr/bin/lamp", "execargs": ["on", "full"], "luascript": ""}


def test_edit_intent_saves_without_reloading_the_page(page, server):
    api = Api(page)
    page.goto(f"{server}/index.html")
    page.select_option("#editSelectintents", "lights off")
    page.click("button:has-text('Edit')")
    expect(page.locator("#editIntentForm #name")).to_have_value("lights off")
    page.fill("#editIntentForm #description", "all lights off")
    page.evaluate("window.marker = 1")
    page.click("text=Save changes")
    expect(page.locator(".toast")).to_contain_text("success")
    assert page.evaluate("window.marker") == 1, "the page reloaded"
    body = json.loads(api.sent("/api/edit_custom_intent")[0][3])
    assert body["number"] == 2 and body["description"] == "all lights off" and body["intent"] == "intent_greeting_goodbye"


def test_delete_intent_asks_first(page, server):
    api = Api(page)
    page.goto(f"{server}/index.html")
    page.once("dialog", lambda d: d.dismiss())
    page.click("button:has-text('Delete')")
    assert not api.sent("/api/remove_custom_intent")
    page.once("dialog", lambda d: d.accept())
    page.click("button:has-text('Delete')")
    expect(page.locator(".toast")).to_be_visible()
    assert json.loads(api.sent("/api/remove_custom_intent")[0][3]) == {"number": 1}


def test_alert_becomes_a_toast(page, server):
    Api(page)
    page.goto(f"{server}/index.html")
    page.evaluate("alert('Could not save')")
    expect(page.locator(".toast.error")).to_contain_text("Could not save")


def test_theme_toggle_cycles_and_is_remembered(page, server):
    Api(page)
    page.goto(f"{server}/index.html")
    page.click("#themeToggle")
    assert page.evaluate("document.documentElement.dataset.theme") == "light"
    page.click("#themeToggle")
    assert page.evaluate("document.documentElement.dataset.theme") == "dark"
    page.goto(f"{server}/setup.html")
    assert page.evaluate("document.documentElement.dataset.theme") == "dark"
    page.click("#themeToggle")
    assert page.evaluate("document.documentElement.dataset.theme") is None


def test_appearance_accent_and_font(page, server):
    Api(page)
    page.goto(f"{server}/index.html#appearance")
    page.select_option("#accent-color-choose", "purple")
    page.select_option("#body-font-choose", "retro")
    assert page.evaluate("document.documentElement.dataset.accent") == "purple"
    assert page.evaluate("document.documentElement.dataset.font") == "retro"
    page.select_option("#accent-color-choose", "teal")
    assert page.evaluate("document.documentElement.dataset.accent") is None


def test_no_page_loads_third_party_scripts(page, server):
    Api(page)
    hosts = set()
    page.on("request", lambda r: hosts.add(urllib.parse.urlparse(r.url).hostname))
    for path in ["index.html", "setup.html", "initial.html", "sdkapp/index.html",
                 "sdkapp/settings.html?serial=0dd1b652", "sdkapp/control.html?serial=0dd1b652"]:
        page.goto(f"{server}/{path}")
        page.wait_for_timeout(200)
    assert hosts == {"127.0.0.1"}
    assert page.errors == []


def test_assistant_settings_load_masked_and_save_the_same_payload(page, server):
    api = Api(page, kg={"provider": "anthropic", "key": "sk-secret", "model": "", "openai_prompt": "",
                        "intentgraph": True, "save_chat": False, "commands_enable": True})
    page.goto(f"{server}/setup.html")
    expect(page.locator("#anthropicKey")).to_have_value("sk-secret")
    assert page.get_attribute("#anthropicKey", "type") == "password"
    page.fill("#anthropicModel", "claude-sonnet-5-5")
    page.click("text=Save assistant settings")
    body = json.loads(api.sent("/api/set_kg_api")[0][3])
    assert body["provider"] == "anthropic" and body["key"] == "sk-secret" and body["model"] == "claude-sonnet-5-5"
    assert body["intentgraph"] is True and body["commands_enable"] is True and body["save_chat"] is False


def test_robot_settings_show_the_robot_state(page, server):
    Api(page)
    page.goto(f"{server}/sdkapp/settings.html?serial=0dd1b652")
    expect(page.locator("#Medium")).to_be_checked()
    expect(page.locator("[id='Azure Blue']")).to_be_checked()
    expect(page.locator("[id='24 Hour']")).to_be_checked()
    expect(page.locator("#tzInput")).to_have_value("America/Toronto")
    expect(page.locator("#robotOffline")).to_be_hidden()
    expect(page.locator(".batteryContainer .tooltip")).to_contain_text("On the charger")


def test_robot_setting_change_posts_for_this_robot(page, server):
    api = Api(page)
    page.goto(f"{server}/sdkapp/settings.html?serial=0dd1b652")
    page.click("label:has(#High)")
    page.wait_for_timeout(200)
    method, _, query, _ = api.sent("/api-sdk/volume")[0]
    assert method == "POST" and query == {"volume": ["5"], "serial": ["0dd1b652"]}


def test_unreachable_robot_shows_a_notice(page, server):
    Api(page, robot_online=False)
    page.goto(f"{server}/sdkapp/settings.html?serial=0dd1b652")
    expect(page.locator("#robotOffline")).to_be_visible()
    page.goto("about:blank")  # a hash-only change would not reload the page
    page.goto(f"{server}/sdkapp/settings.html?serial=0dd1b652#photos")
    expect(page.locator("#photoSection")).to_contain_text("not answering")
    assert page.errors == []


def test_each_photo_deletes_itself(page, server):
    api = Api(page, photos=[11, 22, 33])
    page.goto(f"{server}/sdkapp/settings.html?serial=0dd1b652#photos")
    page.once("dialog", lambda d: d.accept())
    page.locator(".photo").nth(0).locator("button").click()
    page.wait_for_timeout(200)
    assert api.sent("/api-sdk/delete_image")[0][2]["id"] == ["11"]


def test_say_text_keeps_symbols(page, server):
    api = Api(page)
    page.goto(f"{server}/sdkapp/control.html?serial=0dd1b652")
    page.click("label:has(#assume_control)")
    page.fill("#textSay", "salt & pepper? yes")
    page.click("text=Say it")
    page.wait_for_timeout(200)
    assert api.sent("/api-sdk/say_text")[0][2]["text"] == ["salt & pepper? yes"]


def test_keyboard_driving_shows_the_keys(page, server):
    Api(page)
    page.goto(f"{server}/sdkapp/control.html?serial=0dd1b652")
    page.click("label:has(#assume_control)")
    page.click("label:has(#key_control_on)")
    expect(page.locator("#keysKey")).to_be_visible()
    assert page.errors == []
