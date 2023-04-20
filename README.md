Create http server listening on unix sockets or systemd socket activated fds

## Quick Usage

Just replace `http.ListenAndServe` with `anyhttp.ListenAndServe`.

```diff
- http.ListenAndServe(addr, h)
+ anyhttp.ListenAndServe(addr, h)
```

### Address Syntax

#### Unix socket

Syntax

    unix/<path to socket>

Examples

    unix/relative/path.sock
    unix//var/run/app/absolutepath.sock

#### Systemd Socket activated fd:

Syntax

    sysd/fdidx/<fd index starting at 0>
    sysd/fdname/<fd name set using FileDescriptorName socket setting >

Examples:
    
    # First (or only) socket fd passed to app
    sysd/fdidx/0

    # Socket with FileDescriptorName
    sysd/fdname/myapp

    # Using default name
    sysd/fdname/myapp.socket

#### TCP port

If the address is a number less than 65536, it is assumed as a port and passed as `http.ListenAndServe(":<port>",...)`

Anything else is directly passed to `http.ListenAndServe` as well. Below examples should work

    :http
    :8888
    127.0.0.1:8080
