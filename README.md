Create http server listening on unix sockets and systemd socket activated fds

## Quick Usage

    go get go.balki.me/anyhttp

Just replace `http.ListenAndServe` with `anyhttp.ListenAndServe`.

```diff
- http.ListenAndServe(addr, h)
+ anyhttp.ListenAndServe(addr, h)
```

## Address Syntax

### Unix socket

Syntax

    unix?path=<socket_path>&mode=<socket file mode>&remove_existing=<yes|no>

Examples

    unix?path=relative/path.sock
    unix?path=/var/run/app/absolutepath.sock
    unix?path=/run/app.sock&mode=600&remove_existing=no

| option          | description                                    | default  |
|-----------------|------------------------------------------------|----------|
| path            | path to unix socket                            | Required |
| mode            | socket file mode                               | 666      |
| remove_existing | Whether to remove existing socket file or fail | true     |

### Systemd Socket activated fd:

Syntax

    sysd?idx=<fd index>&name=<fd name>&check_pid=<yes|no>&unset_env=<yes|no>&idle_timeout=<duration>

Only one of `idx` or `name` has to be set

Examples:

    # First (or only) socket fd passed to app
    sysd?idx=0

    # Socket with FileDescriptorName
    sysd?name=myapp

    # Using default name and auto shutdown if no requests received in last 30 minutes
    sysd?name=myapp.socket&idle_timeout=30m

| option       | description                                                                                | default          |
|--------------|--------------------------------------------------------------------------------------------|------------------|
| name         | Name configured via FileDescriptorName or socket file name                                 | Required         |
| idx          | FD Index. Actual fd num will be 3 + idx                                                    | Required         |
| idle_timeout | time to wait before shutdown. [syntax][0]                                                  | no auto shutdown |
| check_pid    | Check process PID matches LISTEN_PID                                                       | true             |
| unset_env    | Unsets the LISTEN\* environment variables, so they don't get passed to any child processes | true             |

### TCP

If the address is not one of above, it is assumed to be tcp and passed to `http.ListenAndServe`.

Examples:

    :http
    :8888
    127.0.0.1:8080

## Documentation

https://pkg.go.dev/go.balki.me/anyhttp

### Related links

  * https://gist.github.com/teknoraver/5ffacb8757330715bcbcc90e6d46ac74#file-unixhttpd-go
  * https://github.com/coreos/go-systemd/tree/main/activation

[0]: https://pkg.go.dev/time#ParseDuration
