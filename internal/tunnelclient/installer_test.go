package tunnelclient

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestInstallFailsClosedWithoutDigest(t *testing.T) {
	name := "tunnel-client-v0.0.13-" + runtime.GOOS + "-" + runtime.GOARCH + ".zip"
	i:=NewInstaller(t.TempDir())
	_,err:=i.install(context.Background(),Release{TagName:"v0.0.13",Assets:[]Asset{{Name:name,URL:"https://invalid.example/asset.zip"}}})
	if err==nil || !strings.Contains(err.Error(),"SHA-256") { t.Fatalf("expected missing digest error, got %v",err) }
}
