# gurl

gurl是一个支持命令行和web界面的请求测试工具。请求内容以文本格式存储，方便版本管理。

请求内容示例：

```http
GET http://localhost:8080/hello

###

POST http://localhost:8080/hello
Content-Type: application/json

{
    "name": "gurl"
}
```

## 设计

- reqparser，http文件解析为request对象
- httpclient，执行http请求，返回response对象
- resprinter，支持将respose对象按照不同方式输出

## 快速上手

### web界面

双击`gurl.exe`可启动webserver，访问`http://localhost:7777`执行http请求。

### 命令行

使用`gurl --cli [filepath]`命令，可以执行filepath文件中的请求。也支持重定向输入或管道输入，例如：`echo "get https://www.baidu.com" | gurl --cli`。
