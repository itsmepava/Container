// Exercises every button and form in the web app, as each role, against a
// freshly seeded server, and fails on any error flash, error page, JS error or
// console error. Forms are found by what they do (their action URL), not by
// how they look, so this survives redesigns.
//
//   BASE_URL=http://localhost:8090 node e2e/actions.js
const { chromium } = require('playwright');
const assert = require('assert');

const BASE = process.env.BASE_URL || 'http://localhost:8090';
const pdf = name => ({ name, mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n% ' + name + '\n%%EOF\n') });
const problems = [];
let current = '';

function step(msg) { current = msg; console.log('•', msg); }

async function session(browser, email) {
  const ctx = await browser.newContext({ viewport: { width: 1360, height: 900 } });
  const page = await ctx.newPage();
  page.on('dialog', d => d.accept());
  page.on('pageerror', e => problems.push(`[${current}] JS error: ${e.message}`));
  page.on('console', m => { if (m.type() === 'error' && !/favicon/.test(m.text())) problems.push(`[${current}] console: ${m.text()}`); });
  page.on('response', r => { if (r.status() >= 500) problems.push(`[${current}] HTTP ${r.status()} ${r.url()}`); });
  if (email) {
    await page.goto(BASE + '/login');
    await page.fill('input[name=email]', email);
    await page.fill('input[name=password]', 'eimsky-demo');
    await Promise.all([page.waitForNavigation(), page.click('form[action="/login"] button[type=submit]')]);
    assert(!page.url().includes('/login'), 'sign in as ' + email);
  }
  return page;
}

// result reads the flash after a form post; fails on a bad flash or error page.
async function result(page, expect) {
  await page.waitForLoadState('load');
  const flash = page.locator('[data-flash]');
  const text = (await flash.count()) ? (await flash.first().textContent()).trim() : '';
  const kind = (await flash.count()) ? await flash.first().getAttribute('data-flash') : '';
  if (await page.locator('[data-error-page]').count()) problems.push(`[${current}] error page at ${page.url()}`);
  if (kind === 'bad') problems.push(`[${current}] error: ${text}`);
  else if (expect && !text.includes(expect)) problems.push(`[${current}] expected "${expect}", got "${text || '(no message)'}"`);
  return text;
}

function form(page, action) { return page.locator(`form[action="${action}"]`).first(); }

async function submit(page, action, expect, fill) {
  const f = form(page, action);
  if (!(await f.count())) { problems.push(`[${current}] no form ${action} on ${page.url()}`); return ''; }
  if (fill) await fill(f);
  const btn = f.locator('button[type=submit], button:not([type])').first();
  await Promise.all([page.waitForNavigation(), btn.click()]);
  return result(page, expect);
}

async function upload(page, action, kind, expect) {
  const input = page.locator(`form[action="${action}"]:has(input[name=kind][value=${kind}]) input[type=file]`).first();
  if (!(await input.count())) { problems.push(`[${current}] no upload for ${kind} on ${page.url()}`); return; }
  await Promise.all([page.waitForNavigation(), input.setInputFiles(pdf(kind + '.pdf'))]);
  await result(page, expect);
}

async function autoselect(page, selector, value, expect) {
  const el = page.locator(selector).first();
  if (!(await el.count())) { problems.push(`[${current}] no control ${selector} on ${page.url()}`); return; }
  await Promise.all([page.waitForNavigation(), el.selectOption(value)]);
  await result(page, expect);
}

async function openDialogFor(page, action) {
  const f = form(page, action);
  const dlg = f.locator('xpath=ancestor::dialog');
  if (await dlg.count()) {
    const id = await dlg.getAttribute('id');
    await page.click(`[data-open="${id}"]`);
  }
  return f;
}

(async () => {
  const browser = await chromium.launch();
  try {
    // ---------------- Factory ----------------
    const fac = await session(browser, 'factory@eimsky.test');

    step('factory: book, hold, cancel the hold');
    await fac.goto(BASE + '/f/book');
    const fillCargo = async () => {
      const row = fac.locator('#cargo tbody tr').first();
      for (const [k, v] of Object.entries({ hs: '6006.32', pieces: 10, length: 100, width: 50, height: 40, kg: 20 })) await row.locator(`[name=${k}]`).fill(String(v));
      await fac.waitForTimeout(900);
      // When the pooled box is full, booking needs the shipper's "book anyway" consent.
      const over = fac.locator('input[name=overflow]');
      if (await over.count()) await over.check();
    };
    await fillCargo();
    await submit(fac, '/f/book');
    assert(/\/f\/bookings\/\d+$/.test(fac.url()), 'hold page');
    const heldURL = fac.url();
    let f = await openDialogFor(fac, new URL(heldURL).pathname + '/cancel');
    await Promise.all([fac.waitForNavigation(), f.locator('button:not([type=button])').click()]);
    await result(fac, 'cancelled');

    step('factory: book and confirm');
    await fac.goto(BASE + '/f/book');
    await fillCargo();
    await submit(fac, '/f/book');
    const bookingPath = new URL(fac.url()).pathname;
    await submit(fac, bookingPath + '/confirm', 'Booked');

    step('factory: upload, replace documents');
    await upload(fac, bookingPath + '/docs', 'commercial_invoice', 'uploaded');
    await upload(fac, bookingPath + '/docs', 'packing_list', 'uploaded');
    await upload(fac, bookingPath + '/docs', 'commercial_invoice', 'uploaded');

    step('factory: back button keeps buttons usable');
    await fac.goto(BASE + '/f/bookings');
    await fac.goto(BASE + bookingPath);
    await fac.goBack();
    await fac.goForward();
    await upload(fac, bookingPath + '/docs', 'import_licence', 'uploaded');

    step('factory: book again prefill');
    await fac.goto(BASE + '/f/book?rebook=' + bookingPath.split('/').pop());
    assert.strictEqual(await fac.locator('#cargo [name=hs]').first().inputValue(), '6006.32');

    step('factory: lists and invoice');
    for (const p of ['/f/bookings', '/f/bookings?tab=past', '/f/invoices', '/notifications']) await fac.goto(BASE + p);
    await fac.goto(BASE + '/f/invoices');
    const inv = fac.locator('a[href$="invoice.pdf"]').first();
    if (await inv.count()) {
      const r = await fac.request.get(BASE + (await inv.getAttribute('href')));
      if (r.status() !== 200) problems.push('[invoice] status ' + r.status());
    } else problems.push('[factory invoices] no invoice links');

    step('factory: team invite, deactivate, reactivate');
    await fac.goto(BASE + '/f/team');
    f = await openDialogFor(fac, '/f/team/invite');
    await f.locator('[name=name]').fill('Nimali Fernando');
    await f.locator('[name=email]').fill('nimali@ceylonweave.test');
    await Promise.all([fac.waitForNavigation(), f.locator('button:not([type=button])').click()]);
    await result(fac, 'Invitation sent');
    // Submit again after Back: the button must not stay disabled.
    await fac.goBack();
    f = await openDialogFor(fac, '/f/team/invite');
    await f.locator('[name=name]').fill('Kavindi Silva');
    await f.locator('[name=email]').fill('kavindi@ceylonweave.test');
    await Promise.all([fac.waitForNavigation(), f.locator('button:not([type=button])').click()]);
    await result(fac, 'Invitation sent');
    const deact = fac.locator('form[action$="/active"]').first();
    const deactAction = await deact.getAttribute('action');
    await submit(fac, deactAction, 'Saved');
    await submit(fac, deactAction, 'Saved');

    step('factory: account');
    await fac.goto(BASE + '/account');
    await submit(fac, '/account/profile', 'Profile saved');
    await submit(fac, '/account/company', 'Company details saved', async f => { await f.locator('[name=currency]').selectOption('LKR'); });
    await submit(fac, '/account/password', 'Password changed', async f => {
      await f.locator('[name=current]').fill('eimsky-demo');
      await f.locator('[name=next]').fill('eimsky-demo');
      await f.locator('[name=confirm]').fill('eimsky-demo');
    });
    await fac.goto(BASE + '/account');
    await Promise.all([fac.waitForNavigation(), fac.locator('form[action="/account/notify"] input[type=checkbox]').click()]);
    await result(fac, 'Email notifications');

    step('factory: notifications');
    await fac.goto(BASE + '/notifications');
    if (await form(fac, '/notifications/read').count()) await submit(fac, '/notifications/read');

    // ---------------- Ops ----------------
    const ops = await session(browser, 'ops@eimsky.test');

    step('ops: pools board and own-box decision');
    await ops.goto(BASE + '/ops/pools');
    await ops.locator('a[href*="&s="]').last().click();
    await ops.waitForLoadState('load');
    const decide = ops.locator('form[action$="/decide"]:has(input[value=own])').first();
    if (await decide.count()) {
      await Promise.all([ops.waitForNavigation(), decide.locator('button').click()]);
      await result(ops, 'Own box');
    } else problems.push('[ops pools] no decision form');

    step('ops: planner vessel, containers, placing');
    const planSailing = await (async () => {
      await ops.goto(BASE + '/ops/plan');
      // Find the planner with an overflow lot.
      for (const id of [8, 9, 7]) {
        await ops.goto(`${BASE}/ops/sailings/${id}/plan`);
        if (await ops.locator('form[action$="/move"]').count()) return id;
      }
      return 8;
    })();
    const plan = `/ops/sailings/${planSailing}/plan`;
    await ops.goto(BASE + plan);
    const vessels = ops.locator(`form[action="/ops/sailings/${planSailing}/vessel"] input[type=radio]`);
    await Promise.all([ops.waitForNavigation(), vessels.nth(0).check()]);
    await result(ops, 'Vessel updated');
    await submit(ops, `/ops/sailings/${planSailing}/containers`, 'Container added');
    const containerForms = ops.locator('form[action^="/ops/containers/"]:not([action$="/remove"])');
    const c2 = await containerForms.nth(1).getAttribute('action');
    await autoselect(ops, `form[action="${c2}"] select[name=box]`, '40GP', 'Container 2 is now a 40GP');
    const placeSel = ops.locator('form[action$="/place"] select').last();
    const placeAction = await ops.locator('form[action$="/place"]').last().getAttribute('action');
    const opts = await placeSel.locator('option').evaluateAll(os => os.map(o => o.value));
    await autoselect(ops, `form[action="${placeAction}"] select`, opts[opts.length - 1], 'Lot moved');
    await autoselect(ops, `form[action="${placeAction}"] select`, '0', 'Lot moved');
    await submit(ops, `${c2}/remove`, 'Container removed');
    if (await form(ops, `/ops/sailings/${planSailing}/autoplace`).count()) await submit(ops, `/ops/sailings/${planSailing}/autoplace`, 'Placed');

    step('ops: container options');
    const use = ops.locator('form[action^="/ops/containers/"]:has(input[name=box][value="45HC"]) button');
    if (await use.count()) {
      await Promise.all([ops.waitForNavigation(), use.click()]);
      await result(ops, 'is now a 45HC');
      await Promise.all([ops.waitForNavigation(), ops.locator('form[action^="/ops/containers/"]:has(input[name=box][value="40HC"]) button').first().click()]);
      await result(ops, 'is now a 40HC');
    }

    step('ops: move request asked to the shipper');
    await ops.goto(BASE + plan);
    const moveForms = ops.locator('form[action$="/move"]');
    let movedBooking = '';
    if (await moveForms.count()) {
      movedBooking = (await moveForms.last().getAttribute('action')).split('/')[3];
      await Promise.all([ops.waitForNavigation(), moveForms.last().locator('button').click()]);
      await result(ops, '');
    }

    step('shipper answers the move request');
    if (movedBooking) {
      const j = await session(browser, 'factory10@eimsky.test');
      await j.goto(`${BASE}/f/bookings/${movedBooking}`);
      const accept = j.locator('form[action$="/accept"]');
      if (await accept.count()) {
        await Promise.all([j.waitForNavigation(), accept.locator('button').click()]);
        await result(j, 'moved');
      } else problems.push('[move] shipper sees no move request');
      await j.context().close();
    }

    step('ops: lock the plan');
    await ops.goto(BASE + plan);
    const lock = form(ops, `/ops/sailings/${planSailing}/lock`);
    if (await lock.locator('button[disabled]').count()) {
      problems.push('[lock] still blocked: ' + (await ops.locator('.checks li.no, [data-blocker]').allTextContents()).join(' | '));
    } else {
      await Promise.all([ops.waitForNavigation(), lock.locator('button').click()]);
      await result(ops, 'FCL finalized');
    }

    step('ops: open tender extend, close now, override award');
    await ops.goto(BASE + '/ops/tenders/6');
    await submit(ops, '/ops/tenders/6/extend', 'Deadline updated', async f => {
      const d = new Date(Date.now() + 48 * 3600e3 + 5.5 * 3600e3);
      await f.locator('[name=closes]').fill(d.toISOString().slice(0, 16));
    });
    await submit(ops, '/ops/tenders/6/close', 'Bidding closed');
    const rows = ops.locator('a[href^="?bid="]');
    if ((await rows.count()) >= 2) {
      await rows.nth(1).click();
      await ops.waitForLoadState('load');
      const award = form(ops, '/ops/tenders/6/award');
      if (await award.locator('button[disabled]').count()) {
        // This override would push shippers over their cap, so it must be blocked; award the auto pick.
        await rows.nth(0).click();
        await ops.waitForLoadState('load');
        await submit(ops, '/ops/tenders/6/award', 'Awarded');
      } else {
        await submit(ops, '/ops/tenders/6/award', 'Awarded', async f => {
          const r = f.locator('[name=reason]');
          if (await r.count()) await r.fill('Better record on this lane');
        });
      }
    } else problems.push('[tender 6] fewer than 2 bids');

    step('ops: award closed tender, upload confirmation for the forwarder');
    await ops.goto(BASE + '/ops/tenders/5');
    await submit(ops, '/ops/tenders/5/award', 'Awarded');
    await upload(ops, '/ops/tenders/5/docs', 'booking_confirmation', 'uploaded');

    step('ops: forwarder payables');
    await ops.goto(BASE + '/ops/payables?status=due');
    const pay = ops.locator('form[action$="/payout"]').first();
    if (await pay.count()) {
      const a = await pay.getAttribute('action');
      await submit(ops, a, 'Payment record updated', async f => { await f.locator('[name=ref]').fill('BOC-2291'); });
      await ops.goto(BASE + '/ops/payables?status=paid');
      await submit(ops, a, 'Payment record updated');
    } else problems.push('[payables] nothing to pay');

    step('ops: bookings, booking detail, upload on behalf, cancel');
    await ops.goto(BASE + '/ops/bookings?q=EIM&status=booked');
    await ops.goto(BASE + '/ops/bookings');
    const anyOpen = ops.locator('a[href^="/ops/bookings/"]').first();
    await anyOpen.click();
    await ops.waitForLoadState('load');
    const opsBooking = new URL(ops.url()).pathname;
    if (await ops.locator(`form[action="${opsBooking}/docs"]`).count()) await upload(ops, opsBooking + '/docs', 'packing_list', 'uploaded');

    step('ops: invoices mark paid / unpaid, PDF');
    await ops.goto(BASE + '/ops/invoices');
    const paid = ops.locator('form[action$="/paid"]').first();
    if (await paid.count()) {
      const a = await paid.getAttribute('action');
      await submit(ops, a, 'Invoice updated');
      await submit(ops, a, 'Invoice updated');
    }
    const pdfLink = ops.locator('a[href$=".pdf"]').first();
    if (await pdfLink.count()) {
      const r = await ops.request.get(BASE + (await pdfLink.getAttribute('href')));
      if (r.status() !== 200) problems.push('[ops invoice pdf] ' + r.status());
    }

    // ---------------- Admin ----------------
    const adm = await session(browser, 'admin@eimsky.test');

    step('admin: lanes');
    await adm.goto(BASE + '/admin/lanes/1');
    await submit(adm, '/admin/lanes', 'Lane saved');
    await adm.goto(BASE + '/admin/lanes/new');
    await submit(adm, '/admin/lanes', 'Lane saved', async f => {
      await f.locator('[name=origin]').selectOption('CNSZX');
      await f.locator('[name=dest]').selectOption('LKCMB');
      await f.locator('[name=rate_40HC]').fill('2400');
    });

    step('admin: sailings');
    await adm.goto(BASE + '/admin/lanes');
    const newSailing = adm.locator('a[href^="/admin/sailings/new?lane="]').last();
    await newSailing.click();
    await adm.waitForLoadState('load');
    await submit(adm, '/admin/sailings', 'open for booking', async f => {
      await f.locator('[name=a_name]').fill('MV Test Star');
      await f.locator('[name=a_voyage]').fill('001W');
    });
    const sailingPath = new URL(adm.url()).pathname;
    await submit(adm, sailingPath + '/times', 'Deadlines saved');
    await adm.locator('details:not(.menu) > summary').first().click();
    await submit(adm, sailingPath + '/vessels', 'Vessel added', async f => {
      await f.locator('[name=name]').fill('MV Second');
      const t = new Date(Date.now() + 20 * 86400e3);
      await f.locator('[name=etd]').fill(t.toISOString().slice(0, 16));
      await f.locator('[name=eta]').fill(new Date(t.getTime() + 7 * 86400e3).toISOString().slice(0, 16));
    });
    await submit(adm, sailingPath + '/copy', 'Next week created');

    step('admin: companies approve, reject, create');
    await adm.goto(BASE + '/admin/companies?status=pending');
    await adm.goto(BASE + '/admin/companies/12');
    await submit(adm, '/admin/companies/12/status', 'approved', async f => {});
    await adm.goto(BASE + '/admin/companies/19');
    await adm.click('[data-open="reject-dlg"]');
    const rej = adm.locator('dialog form[action="/admin/companies/19/status"]');
    await rej.locator('[name=note]').fill('Licence photo is unreadable');
    await Promise.all([adm.waitForNavigation(), rej.locator('button:not([type=button])').click()]);
    await result(adm, 'rejected');
    await adm.goto(BASE + '/admin/companies');
    f = await openDialogFor(adm, '/admin/companies');
    await f.locator('[name=name]').fill('Kurunegala Knits');
    await f.locator('[name=contact]').fill('Ruwini Perera');
    await f.locator('[name=email]').fill('ruwini@kknits.test');
    await Promise.all([adm.waitForNavigation(), f.locator('button:not([type=button])').click()]);
    await result(adm, 'Company added');

    step('admin: users');
    await adm.goto(BASE + '/admin/users');
    f = await openDialogFor(adm, '/admin/users/invite');
    await f.locator('[name=name]').fill('Ops Two');
    await f.locator('[name=email]').fill('ops2@eimsky.test');
    await Promise.all([adm.waitForNavigation(), f.locator('button:not([type=button])').click()]);
    await result(adm, 'Invitation sent');
    const ua = await adm.locator('form[action^="/admin/users/"][action$="/active"]').first().getAttribute('action');
    await submit(adm, ua, 'Saved');
    await submit(adm, ua, 'Saved');

    step('admin: settings, container type, port');
    await adm.goto(BASE + '/admin/settings');
    await submit(adm, '/admin/settings', 'Settings saved');
    await adm.locator('details:not(.menu) > summary').nth(0).click();
    await submit(adm, '/admin/container-types', 'Container type saved', async f => {
      await f.locator('[name=code]').fill('20HC');
      await f.locator('[name=name]').fill('High cube');
      await f.locator('[name=internal]').fill('37.4');
      await f.locator('[name=payload]').fill('28000');
    });
    await adm.locator('details:not(.menu) > summary').nth(1).click();
    await submit(adm, '/admin/ports', 'Port saved', async f => {
      await f.locator('[name=code]').fill('CNXMN');
      await f.locator('[name=name]').fill('Xiamen');
    });

    step('admin: HS tariff list import');
    await adm.goto(BASE + '/admin/settings');
    await Promise.all([adm.waitForNavigation(), adm.locator('form[action="/admin/hs-import"] input[type=file]').setInputFiles({ name: 'hs.csv', mimeType: 'text/csv', buffer: Buffer.from('code,description\n845290,Sewing machine parts\n5208.32,Woven cotton, dyed\n') })]);
    await result(adm, 'Imported 2 HS codes');

    step('admin: read-only pages');
    for (const p of ['/admin/audit?q=award', '/dev/mail', '/insights?weeks=4', '/ops/pools', '/ops/tenders?status=awarded', '/ops/payables']) await adm.goto(BASE + p);

    // ---------------- Viewer ----------------
    step('viewer: insights');
    const jaaf = await session(browser, 'jaaf@eimsky.test');
    for (const w of [4, 8, 13, 26]) await jaaf.goto(`${BASE}/insights?weeks=${w}`);

    step('sign out');
    await jaaf.goto(BASE + '/account');
    await Promise.all([jaaf.waitForNavigation(), jaaf.locator('main form[action="/logout"] button').click()]);
    assert(jaaf.url().includes('/login'));
  } catch (e) {
    problems.push(`[${current}] crashed: ${e.message.split('\n')[0]}`);
  } finally {
    await browser.close();
  }
  if (problems.length) {
    console.error('\nPROBLEMS (' + problems.length + '):\n- ' + problems.join('\n- '));
    process.exit(1);
  }
  console.log('ALL ACTIONS PASSED');
})();
