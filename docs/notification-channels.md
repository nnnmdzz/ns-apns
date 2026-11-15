# 企业微信机器人与 Bark

本 fork 在原有 Telegram → APNs 之外增加两个独立渠道。原有配置继续兼容，新渠道默认关闭。下载[自动发布的 Linux 二进制](https://github.com/nnnmdzz/ns-apns/releases/latest)；版本为原版本加序号，例如 `0.1.0-1`。

## 首次部署

按 [部署说明](deploy.md) 安装服务，在首页填写 Telegram api_id / api_hash 并完成扫码。使用与 NodeSeek 官方「新提醒」绑定的同一账号。仅使用企业微信或 Bark 时，不需要填写 Device Token。

默认只监听 `127.0.0.1:8787`。桌面可使用 SSH 隧道访问；要从手机 App 管理，可在自己的 HTTPS 独立域名反向代理到该端口，并先为服务设置访问密码。App 中填写服务根地址，例如 `https://notify.example.com`；渠道页地址为 `/channels`。

## 企业微信机器人

1. 在企业微信群添加机器人，复制完整 Webhook。
2. 服务首页点击「通知渠道」，启用企业微信并填写 Webhook，保存。
3. 点击该卡片的「发送测试通知」。测试只发送到企业微信，不触发 Bark 或 APNs。

使用兼容的 Markdown、摘要和原始 HTTPS 链接。UTF-8 内容不超过 4096 字节，发送间隔至少 3.1 秒。平台明确限频或繁忙时最多尝试 3 次；HTTP 200 还需 `errcode == 0` 才算成功。

## Bark

1. 在手机安装 Bark，允许通知并复制其中的完整推送地址。
2. 在渠道页启用 Bark 并粘贴地址。支持 `https://api.day.app/设备Key`，或自架的 `https://bark.example.com/前缀/设备Key`。
3. 按需设置通知分组、普通/静默/时效性通知。自架启用 Basic Auth 时，在折叠的高级设置中填写用户名和密码。
4. 保存后发送测试通知。仅发送到 Bark；通知会回到安装的 NS Connect 新版原生页面。

通过 JSON POST `/push` 投递，设备 Key 不放在请求路径。通知使用稳定 `id`，新版本 Bark 可替换同一事件；旧版本仍能接收，但替换能力取决于 Bark 和服务端版本。成功要求 JSON `code == 200`。

## 日常维护与升级

- 两个渠道可同时启用。一个失败不会阻止另一个，也不会因另一个失败重送已成功渠道。
- Webhook、设备 Key、Basic Auth 密码不回显。留空保持原值；停用保留凭据，清除配置则移除对应凭据。
- 页面按渠道显示最近成功时间与最新错误。保存后立即生效，不需要重启 Telegram 登录。
- 升级时停止旧进程，备份并保留 `settings.json`、`session.json` 和 `settings.json.deliveries.json`，替换对应架构二进制，再按原方式启动。后者保存最近成功事件与渠道状态，不保存通知正文。
- 成功记录按事件及渠道保留最近 512 项，重启后继续去重。短时排队有容量和等待上限；进程退出会取消尚未投递的请求，不会重放历史通知。
- 网络中断或平台响应异常可能导致结果不明。此时不自动重试，需先核对收件情况，避免重复。不能保证第三方平台精确一次投递。
- 设置文件、Telegram session 与备份属于机密。密钥仅填写在自己的设置页，不提交到 Git，也不要发到聊天中。
- Bark 公共服务会处理明文通知；本版本未实现端到端加密。自架 bark-server 的默认日志可能包含正文和设备 Key，应在自架服务中移除请求正文日志。

## 验证

```sh
go test ./...
go test -race ./internal/channels ./internal/web
python3 -m unittest discover -s scripts -v
```

本机 UI 验收使用模拟设置和模拟测试通知，不连接第三方：

```sh
NS_APNS_PREVIEW=1 go test ./internal/web -run '^TestChannelPreviewServer$' -count=1
# 另开终端，预先安装 playwright 与 WebKit；可用 PLAYWRIGHT_MODULE 指定模块路径
node tools/verify_channels_ui.cjs
```

脚本验证电脑/手机、深色/浅色、保存、独立测试按钮、密钥隐藏和无横向溢出，结束时关闭本机预览服务。真实收件及客户端跳转需自行填写密钥验收。

接口依据：[企业微信官方说明](https://developer.work.weixin.qq.com/document/path/91770)、[官方错误码](https://developer.work.weixin.qq.com/document/path/90313)、[Bark 官方参数](https://github.com/Finb/Bark/blob/master/docs/en-us/params.md)、[Bark 服务端 API](https://github.com/Finb/bark-server/blob/master/docs/API_V2.md)。
