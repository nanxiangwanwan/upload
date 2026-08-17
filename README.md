# go-upload

一个轻量的 Go 文件上传 + 带过期凭证的静态资源服务器。  
A lightweight Go upload server with expiring static-resource access credentials.

## 目录模型 / Directory model

程序安装位置不影响资源目录。资源目录只取决于启动 `upload` 时的当前工作目录。

```text
启动目录 / startup directory = os.Getwd()
真实资源根目录 / actual resource root = <启动目录>/res
对外 URL 前缀 / public URL prefix = /res/
```

例如：

```bash
cd /www/upload
upload
```

真实资源目录：

```text
/www/upload/res
```

如果上传时：

```text
RESPATH=users/avatar
```

最终可能得到：

```text
磁盘文件: /www/upload/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg
客户端路径: /res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg
```

## 配置 / Configuration

```env
ADDR=:8080
MD5_KEY=your-secret-key
TIME_EXPIRE=300
UPLOAD_TYPES=*
MAX_UPLOAD_SIZE=1048576
IP_WHITELIST=
TRUST_PROXY=false
```

完整中英文注释见 `.env.example`，也可以执行：

```bash
upload config
```

生成默认 `.env`：

```bash
upload init
```

## 时间戳 / Timestamps

所有时间戳统一使用 **Unix 秒级时间戳**，例如：

```text
1786660000
```

不要使用 13 位毫秒时间戳。

```bash
date +%s
```

```js
Math.floor(Date.now() / 1000)
```

## 上传认证 / Upload authentication

接口：

```text
POST /upload
Content-Type: multipart/form-data
```

Header：

```text
RESTIME: 1786660000
RESSIGN: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
RESPATH: users/avatar
```

签名规则固定为：

```text
RESSIGN = md5(MD5_KEY + RESTIME)
```

其中：

- `RESTIME` 是**上传请求时间**。
- 服务端使用 `TIME_EXPIRE` 校验请求时间与当前时间的误差。
- `RESPATH` 是目标目录，可不传；只用于决定文件保存目录，不参与签名。
- `/upload` 会验证 **IP 白名单 + 时间 + 签名**。
- 文件放在 `multipart/form-data` 的 `file` 字段。

例如：

```bash
MD5_KEY='your-secret-key'
RESTIME=$(date +%s)
RESPATH='users/avatar'
RESSIGN=$(printf '%s' "${MD5_KEY}${RESTIME}" | md5sum | awk '{print $1}')

curl -X POST 'http://127.0.0.1:8080/upload' \
  -H "RESTIME: ${RESTIME}" \
  -H "RESSIGN: ${RESSIGN}" \
  -H "RESPATH: ${RESPATH}" \
  -F 'file=@./photo.jpg'
```

服务器使用 `crypto/rand` 生成随机文件名并保留原扩展名。

有 `RESPATH`：

```text
<启动目录>/res/<RESPATH>/<随机文件名>.<扩展名>
```

没有 `RESPATH`：

```text
<启动目录>/res/<RESTIME>/<随机文件名>.<扩展名>
```

返回示例：

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "path": "/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg",
    "size": 12345,
    "url": "/res/users/avatar/93f26c0d8fe6407daa89241e72c7e815.jpg"
  }
}
```

## 静态资源认证 / Resource authentication

资源访问和上传是**两套独立认证规则**。

资源访问签名：

```text
RESSIGN = md5(MD5_KEY + RESTIME)
```

这里的 `RESTIME` 不是请求时间，而是：

```text
资源访问凭证的过期时间
Resource access expiration time
```

例如业务后端在用户登录时签发一个 1 小时有效的凭证：

```text
RESTIME=1786663600
RESSIGN=md5(MD5_KEY + "1786663600")
```

在 `RESTIME` 过期之前，这一组 `RESTIME + RESSIGN` 可以访问**所有 `/res/*` 资源**，不需要每个文件单独签名：

```text
/res/user/head/a.jpg?time=1786663600&sign=xxxx
/res/banner/b.jpg?time=1786663600&sign=xxxx
/res/product/c.pdf?time=1786663600&sign=xxxx
```

资源访问：

- **不验证 IP 白名单**，客户端可直接访问。
- 验证 `RESTIME` 是否过期。
- 验证 `RESSIGN = md5(MD5_KEY + RESTIME)`。
- `TIME_EXPIRE` 不参与资源访问校验。
- 同一组有效凭证可访问全部 `/res/*`。

支持 Header：

```text
RESTIME: 1786663600
RESSIGN: xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

也支持 Query，方便 `<img>`、PDF、浏览器直接加载：

```text
/res/user/head/a.jpg?time=1786663600&sign=xxxxxxxx
```

如果当前 Unix 时间已经大于 `RESTIME`，返回：

```text
HTTP 401 Unauthorized
resource access expired
```

签名错误返回：

```text
HTTP 401 Unauthorized
invalid resource signature
```

### 推荐业务流程 / Recommended flow

```text
浏览器
  │
  │ 上传文件
  ▼
业务后端 ──生成上传签名──> /upload
  │                         文件服务器
  │
  └─ 用户登录时生成资源 RESTIME + RESSIGN
                     │
                     ▼
浏览器直接访问 /res/*?time=...&sign=...
```

`MD5_KEY` 只保存在业务后端和文件服务器，不要放到前端 JavaScript 中。

## IP 白名单 / IP whitelist

IP 白名单**只用于上传接口 `/upload`**：

```env
IP_WHITELIST=127.0.0.1,192.168.1.10,10.0.0.8
```

留空表示上传接口不限制 IP：

```env
IP_WHITELIST=
```

静态资源 `/res/*` 不校验 IP 白名单。

## CLI

```bash
upload version
upload config
upload init
upload start
upload start -config /path/to/.env
```

典型部署：

```bash
mkdir -p /www/upload
cd /www/upload
upload init
# 修改 .env 中的 MD5_KEY
upload
```

真实资源目录自动使用：

```text
/www/upload/res
```

## 编译 / Build

```bash
./build.sh
```

生成：

```text
dist/upload-linux-amd64
dist/upload-linux-arm64
dist/upload-darwin-amd64
dist/upload-darwin-arm64
dist/upload-windows-amd64.exe
dist/SHA256SUMS
```

## Gitee 一键安装 / One-command install

`install.sh` 直接从仓库 `master/dist` 下载 Linux 对应架构的二进制：

```bash
curl -fsSL https://gitee.com/cocosnodejs/upload/raw/master/install.sh | sh
```

默认安装到：

```text
/usr/local/bin/upload
```

安装位置不会影响资源目录。
