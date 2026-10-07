const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { fixture } = require("./fixtures/caller-keys.cjs");

const error = "Create an enabled gateway key in Gateway → Gateway keys before sharing on the local network";
for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "ja", "de"]) {
    test(`${engine} ${lang}: sharing needs an explicitly created, enabled key`, async (t) => {
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
      assert.equal((await sharing(true)).status(), 400);
      await page.waitForFunction((error) => document.querySelector("#status").textContent === error, words.error);
      assert.equal(await page.locator("#lanList .lan-address-row").count(), 0);
      assert.equal(await page.locator("#lanList .segs .on").textContent(), words.off);
      assert.equal(events.filter((e) => e.action === "add-key").length, 0, "the switch creates nothing");
      const keys = page.locator("#lanList").getByRole("button", { name: words.keys, exact: true });
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-sharing-needs-key.png`) });
      }
      await keys.click();
      await page.locator("#gatewayKeysBlock").waitFor({ state: "visible" });
      await page.locator("#addGatewayKey").click();
      await page.getByRole("textbox", { name: words.name, exact: true }).fill("Remote laptop");
      await page.getByRole("button", { name: words.create, exact: true }).click();
      await page.locator('#gatewayKeys .acc[data-key="new-key-1"]').waitFor();
      const before = await page.locator("#gatewayKeys .acc[data-key]").evaluateAll((rows) => rows.map((r) => ({
        id: r.dataset.key, name: r.querySelector(".rename").textContent, masked: r.querySelector(".plan").textContent,
      })));
      const network = async () => {
        await page.locator("#prefs").click();
        await page.locator("#setTab-network").click();
        await page.locator("#lanList .segs").waitFor();
      };
      await network();
      for (const on of [true, false, true]) assert.equal((await sharing(on)).status(), 200);
      await keys.click();
      await page.locator('#gatewayKeys .acc[data-key="new-key-1"]').waitFor();
      const after = await page.locator("#gatewayKeys .acc[data-key]").evaluateAll((rows) => rows.map((r) => ({
        id: r.dataset.key, name: r.querySelector(".rename").textContent, masked: r.querySelector(".plan").textContent,
      })));
      assert.deepEqual(after, before, "repeated sharing preserves the explicitly created key");
      await page.locator('#gatewayKeys .acc[data-key="new-key-1"]').getByRole("button", { name: words.disable, exact: true }).click();
      await page.locator('#gatewayKeys .acc.off[data-key="new-key-1"]').waitFor();
      await network();
      assert.equal((await sharing(false)).status(), 200);
      assert.equal((await sharing(true)).status(), 400);
      await page.waitForFunction((error) => document.querySelector("#status").textContent === error, words.error);
      await keys.click();
      await page.locator('#gatewayKeys .acc.off[data-key="new-key-1"]').waitFor();
      assert.equal(await page.locator("#gatewayKeys .acc[data-key]").count(), 1);
      assert.equal(events.filter((e) => e.action === "add-key").length, 1, "only Create issued a key");
      assert.equal(events.filter((e) => ["on-key", "rotate-key"].includes(e.action)).length, 0);
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      assert.deepEqual(errors, []);
    });
  }
}
