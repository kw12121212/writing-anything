net/http 文档：Client 的 Transport 会缓存连接以便复用。调用方必须读完并关闭响应体，连接才能被复用。
Go 1.22 起，for 循环的每次迭代都会创建新的循环变量。
