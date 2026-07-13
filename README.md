# wikiproxy-go

A Go implementation of [wikiproxy](https://github.com/m1stadev/wikiproxy).

Disclaimer that I did not write a single line of this code lol.

This project is based on:

- m1stadev's wikiproxy for The Apple Wiki implementation
- tihmstar's wikiproxy script from libipatcher for the iPhone Wiki implementation

The code is written with Go 1.14 compatibility in mind, as Go 1.14 is the last release that supports OS X 10.11 El Capitan.

## Building

### The Apple Wiki version

```
go build main.go
```

### The iPhone Wiki version

Initialize the module and install the required dependency:

```
go mod init wikiproxy-go-ipw
go get github.com/PuerkitoBio/goquery
```

If you're building with Go 1.14, use the last compatible release of goquery:

```
go get github.com/PuerkitoBio/goquery@v1.8.1
```

Then build:

```
go build main-ipw.go
```
