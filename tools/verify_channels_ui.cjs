const fs = require('node:fs');
const assert = require('node:assert/strict');
const { webkit } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const base = process.env.CHANNELS_PREVIEW_URL || fs.readFileSync('/private/tmp/ns-apns-preview-url', 'utf8');
assert(['127.0.0.1', 'localhost'].includes(new URL(base).hostname), 'Only a local fixture server is allowed');
const output = process.env.PREVIEW_OUTPUT || '/private/tmp/ns-apns-channel-ui';
fs.mkdirSync(output, { recursive: true });
(async () => {
  const browser = await webkit.launch({ headless: true, executablePath: process.env.PLAYWRIGHT_WEBKIT_EXECUTABLE });
  try {
    const setup = await browser.newPage();
    await setup.goto(base + '/channels');
    await setup.locator('#wecom input[name=enabled]').check();
    await setup.locator('#wecom-address').fill('https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=fake-preview-wecom');
    await setup.locator('#wecom button.primary').click();

    await setup.getByText('渠道设置已保存并生效。').waitFor();
    await setup.locator('#bark input[name=enabled]').check();
    await setup.locator('#bark-address').fill('https://bark.example.com/prefix/fake-preview-device');
    await setup.locator('#bark button.primary').click();
    await setup.getByText('渠道设置已保存并生效。').waitFor();
    for (const name of ['wecom', 'bark']) {
      await setup.locator(`#${name} button`, { hasText: '发送测试通知' }).click();
      await setup.getByText('测试通知已发送，请在对应应用中确认。').waitFor();
    }
    assert(!(await setup.content()).includes('fake-preview-wecom'));
    assert(!(await setup.content()).includes('fake-preview-device'));
    await setup.close();
    for (const [device, viewport] of [['mobile', {width: 390, height: 844}], ['desktop', {width: 1280, height: 860}]]) {
      for (const colorScheme of ['light', 'dark']) {
        const page = await browser.newPage({viewport, colorScheme});
        const errors = [];
        page.on('pageerror', error => errors.push(error.message));
        await page.goto(base + '/channels');
        assert.equal(await page.locator('h2').count(), 2);
        assert(await page.locator('#wecom input[name=enabled]').isChecked());
        assert(await page.locator('#bark input[name=enabled]').isChecked());
        assert.equal(await page.locator('#wecom-address').inputValue(), '');
        assert.equal(await page.locator('#bark-address').inputValue(), '');
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
        await page.screenshot({path: `${output}/${device}-${colorScheme}.png`, fullPage: true});
        assert.deepEqual(errors, []);
        await page.close();
      }
    }
    console.log('Passed: 4 viewport/theme screenshots, channel save, isolated test actions, secret masking, no overflow.');
  } finally {
    await browser.close();
    await fetch(base + '/__stop', {method: 'POST'});
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
