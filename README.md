# Fiber with Glojure

A proof of concept web server written in **Go** using the [Fiber](https://gofiber.io/) web framework.
Handlers are written in an embedded [Glojure](github.com/glojurelang/glojure) interpreter.

```bash
$ make run
go run main.go
2026/05/04 11:28:09 loading src/app/core.clj
2026/05/04 11:28:09 listening on http://127.0.0.1:3000

 ┌───────────────────────────────────────────────────┐ 
 │                  Fiber v2.52.10                   │ 
 │               http://127.0.0.1:3000               │ 
 │       (bound on host 0.0.0.0 and port 3000)       │ 
 │                                                   │ 
 │ Handlers ............ 11  Processes ........... 1 │ 
 │ Prefork ....... Disabled  PID ............. 91777 │ 
 └───────────────────────────────────────────────────┘ 
```


```bash
$ curl -s 'http://localhost:3000?name=glojure'
<h1>Hello, glojure!</h1>
```
