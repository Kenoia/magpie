const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { fixture } = require("./fixtures/caller-keys.cjs");

const error = "Create or enable a gateway key before sharing";
for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "ja", "de"]) {
    test(`${engine} ${lang}: sharing creates only the first key and preserves existing keys`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width: 560, height: 740 }, reducedMotion: "reduce" })).newPage();
      page.setDefaultTimeout(6000);
      const events = [], errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.route("**/*", fixture(lang, "light", events, { keys: [] }));
      await page.goto("http://magpie.test/?view=settings&tab=network");
      await page.locator("#lanList .segs").waitFor();
      const words = await page.evaluate((error) => ({ on: t("On"), off: t("Off"), keys: t("Gateway keys"),
        name: t("Gateway key name"), create: t("Create"), disable: t("Disable key"), error: t(error) }), error);
      const sharing = async (on) => {
        const before = await page.locator("#lanList .segs .on").textContent();
        const pending = page.waitForResponse((r) => new URL(r.url()).pathname === "/api/settings/lan");
        await page.locator("#lanList").getByRole("button", { name: on ? words.on : words.off, exact: true }).click();
        const reply = await pending;
        const selected = reply.status() === 200 ? (on ? words.on : words.off) : before;
        await page.waitForFunction((selected) => prefsBusy === 0 &&
          document.querySelector("#lanList .segs .on")?.textContent === selected, selected);
        return reply;
      };
      const network = async () => {
        await page.locator("#prefs").click();
        await page.locator("#setTab-network").click();
        await page.locator("#lanList .segs").waitFor();
      };
      assert.equal((await sharing(false)).status(), 200);
      assert.equal(await page.locator("#lanList .lan-address-row").count(), 0);
      assert.equal(await page.locator("#lanList .segs .on").textContent(), words.off);
      const keys = page.locator("#lanList").getByRole("button", { name: words.keys, exact: true });
      await keys.click();
      await page.locator("#gatewayKeysBlock").waitFor({ state: "visible" });
      const empty = page.locator("#gatewayKeys .empty-state");
      await empty.waitFor();
      const shape = await empty.evaluate((node) => {
        const css = getComputedStyle(node);
        const probe = document.createElement("span");
        probe.style.color = "var(--muted)";
        node.append(probe);
        const muted = css.color === getComputedStyle(probe).color;
        probe.remove();
        return { padding: parseFloat(css.paddingTop), centered: css.textAlign === "center", muted };
      });
      assert.ok(shape.padding >= 20, "the empty key list has the app's padded empty-state spacing");
      assert.equal(shape.centered, true);
      assert.equal(shape.muted, true, "the empty key list uses the app's muted text token");
      await page.setViewportSize({ width: 440, height: 600 });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-empty-gateway-keys.png`) });
      }
      await network();
      assert.equal((await sharing(true)).status(), 200);
      await page.locator("#lanList .lan-address-row").waitFor();
      assert.equal(await page.locator("#lanList [role=alert]").count(), 0);
      await keys.click();
      await page.locator('#gatewayKeys .acc[data-key="lan-key-1"]').waitFor();
      assert.equal(await page.locator("#gatewayKeys .acc[data-key]").count(), 1, "the empty store gets its first key");
      const before = await page.locator("#gatewayKeys .acc[data-key]").evaluateAll((rows) => rows.map((r) => ({
        id: r.dataset.key, name: r.querySelector(".rename").textContent, masked: r.querySelector(".plan").textContent,
      })));
      await network();
      for (const on of [true, false, true]) assert.equal((await sharing(on)).status(), 200);
      assert.equal(await page.locator("#lanList [role=alert]").count(), 0, "a successful toggle clears the guidance");
      await keys.click();
      await page.locator('#gatewayKeys .acc[data-key="lan-key-1"]').waitFor();
      const after = await page.locator("#gatewayKeys .acc[data-key]").evaluateAll((rows) => rows.map((r) => ({
        id: r.dataset.key, name: r.querySelector(".rename").textContent, masked: r.querySelector(".plan").textContent,
      })));
      assert.deepEqual(after, before, "repeated sharing preserves the first key");
      await page.locator('#gatewayKeys .acc[data-key="lan-key-1"]').getByRole("button", { name: words.disable, exact: true }).click();
      await page.locator('#gatewayKeys .acc.off[data-key="lan-key-1"]').waitFor();
      await network();
      assert.equal((await sharing(false)).status(), 200);
      assert.equal((await sharing(true)).status(), 400);
      await page.locator("#lanList [role=alert]").filter({ hasText: words.error }).waitFor();
      assert.equal(await page.locator("#status.err").count(), 0, "missing-key guidance stays beside the sharing control");
      if (process.env.ARTIFACT_DIR) {
        await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-sharing-needs-key.png`) });
      }
      await keys.click();
      await page.locator('#gatewayKeys .acc.off[data-key="lan-key-1"]').waitFor();
      assert.equal(await page.locator("#gatewayKeys .acc[data-key]").count(), 1);
      assert.equal(events.filter((e) => e.action === "add-key").length, 0, "sharing does not add another key when the existing key is disabled");
      assert.equal(events.filter((e) => ["on-key", "rotate-key"].includes(e.action)).length, 0);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await network();
      const failure = "fixture: gateway settings could not be saved";
      await page.route("**/api/settings/lan", (route) => route.fulfill({ status: 503, json: { error: failure } }));
      assert.equal((await sharing(true)).status(), 503);
      await page.waitForFunction((failure) => document.querySelector("#status.err")?.textContent === failure, failure);
      assert.equal(await page.locator("#lanList [role=alert]").count(), 0, "other request failures keep their original error feedback");
      assert.equal(await page.locator("#lanList .segs .on").textContent(), words.off);
      await page.unroute("**/api/settings/lan");
      await keys.click();
      await page.locator("#addGatewayKey").click();
      await page.getByRole("textbox", { name: words.name, exact: true }).fill("Remote laptop");
      await page.getByRole("button", { name: words.create, exact: true }).click();
      await page.locator('#gatewayKeys .acc[data-key="new-key-2"]').waitFor();
      await network();
      assert.equal((await sharing(true)).status(), 200);
      assert.equal(await page.locator("#lanList [role=alert]").count(), 0);
      await keys.click();
      await page.locator('#gatewayKeys .acc.off[data-key="lan-key-1"]').waitFor();
      assert.equal(await page.locator("#gatewayKeys .acc[data-key]").count(), 2);
      assert.equal(events.filter((e) => e.action === "add-key").length, 1, "only Create adds the second key");
      assert.equal(events.filter((e) => ["on-key", "rotate-key"].includes(e.action)).length, 0);
      assert.deepEqual(errors, []);
    });
  }
}
