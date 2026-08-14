# upload

一个轻量的 Go 文件上传 + 受保护静态资源服务器。  
A lightweight Go file upload + protected static-resource server.

## 功能 / Features

- `POST /upload` 文件上传 / multipart file upload
- `GET /res/<path>`、`HEAD /res/<path>` 受保护静态资源 / protected static files
- `.env` 配置，无第三方 Go 依赖 / `.env` configuration with no third-party Go dependency
- 上传类型配置，默认全部允许 / configurable upload extensions, all allowed by default
- 文件大小限制，单位字节，默认 1 MiB / upload size limit in bytes, 1 MiB by default
- MD5 + Unix 秒级时间戳签名 / MD5 + Unix-seconds timestamp signature
- 上传和静态资源都支持 IP 白名单 / IP whitelist for both upload and static resources
- 服务端随机生成最终文件名 / server-generated random final filename
- 路径穿越保护 / path traversal protection
- 默认资源目录为可执行文件目录下的 `res/` / default resource root is `res/` next to the executable

## 最终上传路径规则 / Final upload path rules

### 1. 传入 `RESPATH` / With `RESPATH`

`RESPATH` **只表示上传目录，不表示文件名**。  
`RESPATH` **is only the destination directory, not the filename**.

例如 / Example:

```text
RESPATH: users/avatar
上传文件 / Uploaded file: photo.jpg
```

服务端随机生成 16 字节随机数，以 32 位十六进制字符串作为文件名，并保留原扩展名。  
The server generates 16 random bytes, encodes them as a 32-character hex filename, and keeps the original extension.

最终可能保存为 / Final path may be:

```text
/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg
```

不会再自动增加日期或时间目录。  
No extra date/time directory is added when `RESPATH` is supplied.

### 2. 不传 `RESPATH` / Without `RESPATH`

如果没有 `RESPATH` Header，则使用本次请求的 `RESTIME` 作为目录。  
If the `RESPATH` header is omitted, the validated request `RESTIME` is used as the directory.

例如 / Example:

```text
RESTIME: 1786660000
上传文件 / Uploaded file: photo.jpg
```

最终可能保存为 / Final path may be:

```text
/res/1786660000/93f26c0d8fe6407daa89241e72c7e815.jpg
```

这样客户端不需要提前知道服务器生成的随机文件名。  
The client never needs to know the random filename before uploading.

## RESTIME 时间戳格式 / RESTIME timestamp format

`RESTIME` **统一使用 Unix 秒级时间戳**。  
`RESTIME` **must use a Unix timestamp in seconds**.

通常是 10 位数字 / Normally 10 digits:

```text
1786660000
```

不要使用 Unix 毫秒时间戳 / Do not use Unix milliseconds:

```text
1786660000000
```

常见语言 / Common languages:

```bash
# Linux / macOS shell
date +%s
```

```js
// JavaScript
Math.floor(Date.now() / 1000)
```

```go
// Go
time.Now().Unix()
```

```php
// PHP
time()
```

## Header 设计 / Header design

上传认证参数统一放 Header，文件内容放 `multipart/form-data`。  
Authentication metadata stays in headers; the file itself stays in `multipart/form-data`.

```text
RESTIME   Unix 秒级时间戳 / Unix timestamp in seconds
RESSIGN   MD5 签名 / MD5 signature
RESPATH   上传目标目录，可选 / Optional upload destination directory
```

推荐这样做，因为可以在解析大文件 Body 之前先完成 IP 白名单和签名验证。  
This allows IP and signature checks before parsing a potentially large request body.

## 上传签名 / Upload signature

上传签名规则固定为 `MD5_KEY + RESTIME + RESPATH`，不包含最终随机文件名。  
The upload signature is fixed as `MD5_KEY + RESTIME + RESPATH` and does not include the random final filename.

### 有 RESPATH / With RESPATH

例如 / Example:

```text
MD5_KEY=abc123
RESTIME=1786660000
RESPATH=users/avatar
```

参与 MD5 的原始字符串 / Raw MD5 input:

```text
abc1231786660000users/avatar
```

公式 / Formula:

```text
RESSIGN = lowercase_hex(md5(MD5_KEY + RESTIME + RESPATH))
```

> **重要 / Important**：上传验签使用 Header 中的 `RESPATH` 值（去掉首尾空格）直接参与 MD5；文件系统安全检查与路径清理在验签通过后单独进行。上传签名不会复用静态资源的 URL 路径规则。  
> Upload verification signs the `RESPATH` Header value directly (after trimming surrounding whitespace). Filesystem normalization is a separate step after authentication. Upload signing never reuses the static-resource URL-path rule.

### 没有 RESPATH / Without RESPATH

`RESPATH` 按空字符串参与签名。  
`RESPATH` is treated as an empty string for signing.

```text
RESSIGN = lowercase_hex(md5(MD5_KEY + RESTIME))
```

保存目录仍然会自动使用 `RESTIME`：  
The storage directory still automatically becomes `RESTIME`:

```text
/res/1786660000/<random-name>.jpg
```

## RESPATH 清理规则 / RESPATH normalization

`RESPATH` 是相对于 `RES_ROOT` 的目录。服务器会统一：  
`RESPATH` is a directory relative to `RES_ROOT`. The server normalizes it by:

- `\` 转为 `/` / converting `\` to `/`
- 去掉开头 `/` / removing a leading `/`
- 合并重复 `/` / collapsing repeated `/`
- 清理 `.` 路径段 / removing `.` path segments
- 安全解析 `..`，越出资源根目录则拒绝 / resolving safe `..` and rejecting escapes outside the resource root

例如 / Examples:

```text
/users//avatar/./   -> users/avatar
users\avatar        -> users/avatar
users/tmp/../avatar -> users/avatar
../../etc           -> rejected
```

建议调用方直接传简单目录，例如：  
Clients should preferably send a simple directory such as:

```text
users/avatar
article/image
pdf
```

## 上传示例 / Upload example

假设 / Assume:

```text
MD5_KEY=your-secret-key
RESPATH=users/avatar
```

Linux:

```bash
RESTIME=$(date +%s)
RESPATH='users/avatar'
RESSIGN=$(printf '%s' "your-secret-key${RESTIME}${RESPATH}" | md5sum | awk '{print $1}')

curl -X POST 'http://127.0.0.1:8080/upload' \
  -H "RESTIME: ${RESTIME}" \
  -H "RESSIGN: ${RESSIGN}" \
  -H "RESPATH: ${RESPATH}" \
  -F 'file=@./photo.jpg'
```

成功返回 / Successful response:

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "path": "users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg",
    "size": 12345,
    "url": "/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg"
  }
}
```

不传 `RESPATH` 时 / Without `RESPATH`:

```bash
RESTIME=$(date +%s)
RESSIGN=$(printf '%s' "your-secret-key${RESTIME}" | md5sum | awk '{print $1}')

curl -X POST 'http://127.0.0.1:8080/upload' \
  -H "RESTIME: ${RESTIME}" \
  -H "RESSIGN: ${RESSIGN}" \
  -F 'file=@./photo.jpg'
```

返回路径类似 / Returned path will look like:

```text
1786660000/93f26c0d8fe6407daa89241e72c7e815.jpg
```

## 静态资源签名 / Static-resource signature

静态资源访问使用**最终完整 URL 资源路径**参与签名，必须包含 `/res/` 前缀。  
Static-resource access signs the **final full resource URL path**, including the `/res/` prefix.

上传签名和访问签名完全独立。静态资源签名不使用 `RESPATH`；查询参数 `?time=...&sign=...` 也不参与 MD5。  
Upload and resource signatures are fully independent. Static-resource signing does not use `RESPATH`; query parameters such as `?time=...&sign=...` are not included in the MD5 input.

例如文件 / For this file:

```text
/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg
```

公式 / Formula:

```text
RESSIGN = lowercase_hex(md5(MD5_KEY + RESTIME + FINAL_RESOURCE_URL_PATH))
```

例如 / Example:

```text
MD5_KEY=abc123
RESTIME=1786660000
FINAL_RESOURCE_URL_PATH=/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg

参与 MD5 的字符串 / Raw MD5 input:
abc1231786660000/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg
```

请求 Header 模式 / Header mode:

```bash
RESOURCE_PATH="/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg"
RESSIGN=$(printf '%s' "your-secret-key${RESTIME}${RESOURCE_PATH}" | md5sum | awk '{print $1}')

curl \
  -H "RESTIME: ${RESTIME}" \
  -H "RESSIGN: ${RESSIGN}" \
  'http://127.0.0.1:8080/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg'
```

普通浏览器、`<img>`、PDF 链接通常无法自定义 Header，因此静态 GET/HEAD 同时支持 Query：  
Browsers and ordinary `<img>`/PDF links cannot normally add custom headers, so GET/HEAD also supports query authentication:

```text
/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg?time=1786660000&sign=xxxx
```

上传接口仍然只使用 Header 认证。  
The upload endpoint remains header-authenticated.

## IP 白名单 / IP whitelist

**上传接口和静态资源接口都会验证 IP 白名单。**  
**Both upload and static-resource endpoints enforce the IP whitelist.**

并且上传接口会在读取 multipart 文件之前先验证 IP。  
The upload endpoint checks the IP before reading multipart file data.

```env
IP_WHITELIST=127.0.0.1,192.168.1.10,10.0.0.8
```

留空表示不限制 / Empty means unrestricted:

```env
IP_WHITELIST=
```

如果开启：

```env
TRUST_PROXY=true
```

服务端会读取 `X-Real-IP` / `X-Forwarded-For`。只应在你控制的可信反向代理后开启。  
The server will read `X-Real-IP` / `X-Forwarded-For`. Enable this only behind a reverse proxy you control.

## 配置 / Configuration

```env
ADDR=:8080
MD5_KEY=your-secret-key
TIME_EXPIRE=300
UPLOAD_TYPES=*
MAX_UPLOAD_SIZE=1048576
IP_WHITELIST=
RES_ROOT=
TRUST_PROXY=false
```

默认值 / Defaults:

- `ADDR=:8080`
- `UPLOAD_TYPES=*`
- `MAX_UPLOAD_SIZE=1048576` bytes = 1 MiB
- `TIME_EXPIRE=300` seconds
- `RES_ROOT=<executable-directory>/res`
- `IP_WHITELIST=` means no IP restriction
- `MD5_KEY` 必填 / required

完整中英文注释见 `.env.example`。  
See `.env.example` for full bilingual comments.

## CLI 命令 / CLI commands

打印配置说明和默认配置 / Print configuration help and defaults:

```bash
upload config
```

生成带中英文注释的 `.env` / Generate a bilingual `.env`:

```bash
upload init
```

指定路径 / Custom path:

```bash
upload init /etc/go-upload/.env
```

已存在的配置不会被覆盖。  
Existing configuration files are never overwritten.

启动 / Start:

```bash
upload start -config .env
```

或者 / Or:

```bash
upload -config .env
```

版本 / Version:

```bash
upload version
```

## 本地编译 / Build

```bash
go test ./...
go vet ./...
./build.sh
```

生成 / Outputs:

```text
dist/upload-linux-amd64
dist/upload-linux-arm64
dist/upload-darwin-amd64
dist/upload-darwin-arm64
dist/upload-windows-amd64.exe
```

## Gitee Release 安装 / Gitee Release install

把 `dist/` 对应文件上传到 Gitee Release 后，可使用仓库中的 `install.sh`：  
After uploading the matching `dist/` binary to a Gitee Release, use `install.sh`:

```bash
curl -fsSL https://gitee.com/cocosnodejs/upload/raw/master/install.sh | VERSION=v1.2.2 sh
```

默认安装到 / Default install destination:

```text
/usr/local/bin/upload
```

之后即可 / Then run:

```bash
upload version
upload config
upload start -config /etc/go-upload/.env
```

## 安全说明 / Security notes

- 最终文件名使用 `crypto/rand` 生成，不使用 `math/rand`。 / Final filenames use `crypto/rand`, not `math/rand`.
- MD5 是按当前需求实现的共享密钥签名；公网高价值场景更推荐 HMAC-SHA256。 / MD5 is implemented as requested; HMAC-SHA256 is preferable for higher-value public deployments.
- 时间窗口可以缩短重放有效期，但无法彻底阻止窗口内重放。 / The timestamp window limits replay duration but does not eliminate replay inside the window.
- `TRUST_PROXY=true` 只应在可信代理环境开启。 / Enable `TRUST_PROXY=true` only behind a trusted proxy.
