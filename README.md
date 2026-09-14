# go-upload

**Version: v1.8.0**

轻量 Go 文件上传 + 带签名静态资源服务器。

## 安装

```bash
curl -fsSL https://raw.githubusercontent.com/nanxiangwanwan/upload/master/install.sh | sh
```

查看版本：

```bash
upload version
```

## 配置

```env
ADDR=:8080
UPLOAD_PATH=/upload
REQUIRE_TYPE_SIZE=false
MD5_KEY=your-secret-key
TIME_EXPIRE=300
UPLOAD_TYPES=*
MAX_UPLOAD_SIZE=1048576
IP_WHITELIST=
TRUST_PROXY=false
```

### UPLOAD_PATH

上传接口路径可配置，默认：

```env
UPLOAD_PATH=/upload
```

例如：

```env
UPLOAD_PATH=/file/upload
```

## 上传

上传 Header：

```text
RESTIME: Unix 秒级时间戳
RESSIGN: 签名
RESPATH: 可选上传目录
RESTYPE: 可选类型规则
RESSIZE: 可选最大字节数
```

`RESDATA` 已从上传接口删除。

上传签名：

```text
RESSIGN = md5(MD5_KEY + RESTIME + RESTYPE + RESSIZE)
```

如果 `RESTYPE`、`RESSIZE` 都为空：

```text
RESSIGN = md5(MD5_KEY + RESTIME)
```

例如：

```text
RESTYPE=.jpg,.png
RESSIZE=5242880
```

签名：

```text
md5(MD5_KEY + RESTIME + ".jpg,.png" + "5242880")
```

### RESTYPE

`RESTYPE` 非空时，本次上传使用它检查文件扩展名，并覆盖服务器配置：

```env
UPLOAD_TYPES
```

支持：

```text
.jpg,.jpeg,.png
```

也支持：

```text
*
```

### RESSIZE

`RESSIZE` 非空时，本次上传使用它作为文件最大字节数，并覆盖：

```env
MAX_UPLOAD_SIZE
```

例如 5 MiB：

```text
RESSIZE=5242880
```

### REQUIRE_TYPE_SIZE

默认：

```env
REQUIRE_TYPE_SIZE=false
```

`RESTYPE`、`RESSIZE` 可以不传。

如果：

```env
REQUIRE_TYPE_SIZE=true
```

上传必须同时携带：

```text
RESTYPE
RESSIZE
```

少一个都会返回 `400`。

## 上传示例

```bash
MD5_KEY='your-secret-key'
RESTIME=$(date +%s)
RESTYPE='.jpg,.png'
RESSIZE='5242880'
RESPATH='users/avatar'
RESSIGN=$(printf '%s' "${MD5_KEY}${RESTIME}${RESTYPE}${RESSIZE}" | md5sum | awk '{print $1}')

curl -X POST 'http://127.0.0.1:8080/upload' \
  -H "RESTIME: ${RESTIME}" \
  -H "RESSIGN: ${RESSIGN}" \
  -H "RESTYPE: ${RESTYPE}" \
  -H "RESSIZE: ${RESSIZE}" \
  -H "RESPATH: ${RESPATH}" \
  -F 'file=@./photo.jpg'
```

返回：

```json
{
  "code": 0,
  "message": "成功",
  "data": {
    "path": "/res/users/avatar/xxxx.jpg",
    "size": 12345,
    "url": "/res/users/avatar/xxxx.jpg"
  }
}
```

## 静态资源访问

资源访问规则保持不变：

```text
RESSIGN = md5(MD5_KEY + RESTIME + data)
```

如果 URL 中存在 `path` 参数：

```text
RESSIGN = md5(MD5_KEY + RESTIME + data + 资源完整路径)
```

例如：

```text
/res/users/avatar/a.jpg?time=1786663600&data=user-1&path=1&sign=xxxx
```

## Go 代理工具

模块：

```bash
go get github.com/nanxiangwanwan/upload
```

包名：

```go
package zupload
```

Gin 可以直接：

```go
err := zupload.Proxy(
    c.Writer,
    c.Request,
    "http://127.0.0.1:8080",
    "/res/users/avatar/a.jpg",
    "your-md5-key",
)
```

## 编译

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
