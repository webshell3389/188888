.RECIPEPREFIX = >
BIN     := mockc2
PKG     := .
DIST    := dist
LDFLAGS := -s -w

.PHONY: all help windows linux linux-arm64 dist clean

help:
> @echo "make linux          # 构建 Linux amd64 静态二进制 -> dist/mockc2-linux-amd64"
> @echo "make linux-arm64    # 构建 Linux arm64 静态二进制 -> dist/mockc2-linux-arm64"
> @echo "make windows        # 构建 Windows amd64 -> dist/mockc2-windows-amd64.exe"
> @echo "make dist           # 三者全出"
> @echo "make clean"

all: dist

windows:
> CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BIN)-windows-amd64.exe $(PKG)

linux:
> CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BIN)-linux-amd64 $(PKG)

linux-arm64:
> CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/$(BIN)-linux-arm64 $(PKG)

dist: linux linux-arm64 windows

clean:
> rm -rf $(DIST)
