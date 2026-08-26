package tunnelclient

import(
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)
const latestURL="https://api.github.com/repos/openai/tunnel-client/releases/latest"
type Asset struct{Name string `json:"name"`;URL string `json:"browser_download_url"`;Digest string `json:"digest"`}
type Release struct{TagName string `json:"tag_name"`;Assets []Asset `json:"assets"`}
type Active struct{Version string `json:"version"`;Path string `json:"path"`;Digest string `json:"digest"`}
type Installer struct{Root string;Client *http.Client}
func NewInstaller(root string)*Installer{return &Installer{Root:root,Client:&http.Client{Timeout:2*time.Minute}}}
func(i *Installer)CheckLatest(ctx context.Context)(Release,error){req,err:=http.NewRequestWithContext(ctx,http.MethodGet,latestURL,nil);if err!=nil{return Release{},err};req.Header.Set("User-Agent","GPT-Tunnel-Manager");resp,err:=i.Client.Do(req);if err!=nil{return Release{},err};defer resp.Body.Close();if resp.StatusCode!=200{return Release{},fmt.Errorf("release metadata HTTP %s",resp.Status)};var r Release;if err:=json.NewDecoder(io.LimitReader(resp.Body,4<<20)).Decode(&r);err!=nil{return r,err};return r,nil}
func(i *Installer)Ensure(ctx context.Context,override string)(Active,error){if override!=""{if _,err:=os.Stat(override);err!=nil{return Active{},err};return Active{Version:"custom",Path:override},nil};if a,err:=i.readActive();err==nil{if _,err:=os.Stat(a.Path);err==nil{return a,nil}};return i.InstallLatest(ctx)}
func(i *Installer)InstallLatest(ctx context.Context)(Active,error){r,err:=i.CheckLatest(ctx);if err!=nil{return Active{},err};return i.install(ctx,r)}
func(i *Installer)install(ctx context.Context,r Release)(Active,error){asset,err:=selectAsset(r);if err!=nil{return Active{},err};dir:=filepath.Join(i.Root,"tools","tunnel-client",r.TagName);if err:=os.MkdirAll(dir,0700);err!=nil{return Active{},err};tmp,err:=os.CreateTemp(filepath.Join(i.Root,"data"),"tunnel-client-*.zip");if err!=nil{return Active{},err};tmpPath:=tmp.Name();tmp.Close();defer os.Remove(tmpPath);req,_:=http.NewRequestWithContext(ctx,http.MethodGet,asset.URL,nil);req.Header.Set("User-Agent","GPT-Tunnel-Manager");resp,err:=i.Client.Do(req);if err!=nil{return Active{},err};if resp.StatusCode!=200{resp.Body.Close();return Active{},fmt.Errorf("download HTTP %s",resp.Status)};f,err:=os.OpenFile(tmpPath,os.O_WRONLY|os.O_TRUNC,0600);if err!=nil{resp.Body.Close();return Active{},err};h:=sha256.New();_,copyErr:=io.Copy(io.MultiWriter(f,h),io.LimitReader(resp.Body,512<<20));resp.Body.Close();f.Close();if copyErr!=nil{return Active{},copyErr};got:=hex.EncodeToString(h.Sum(nil));want:=strings.TrimPrefix(asset.Digest,"sha256:");if want!=""&& !strings.EqualFold(got,want){return Active{},fmt.Errorf("tunnel-client checksum mismatch")};bin,err:=extractBinary(tmpPath,dir);if err!=nil{return Active{},err};a:=Active{Version:r.TagName,Path:bin,Digest:"sha256:"+got};if err:=i.writeActive(a);err!=nil{return Active{},err};return a,nil}
func selectAsset(r Release)(Asset,error){suffix:=fmt.Sprintf("-%s-%s.zip",runtime.GOOS,runtime.GOARCH);prefix:="tunnel-client-";for _,a:=range r.Assets{if strings.HasPrefix(a.Name,prefix)&&strings.HasSuffix(a.Name,suffix)&&!strings.Contains(a.Name,"runtime-cloudflared")&&!strings.Contains(a.Name,"runtime-"){return a,nil}};return Asset{},fmt.Errorf("no tunnel-client asset for %s/%s",runtime.GOOS,runtime.GOARCH)}
func extractBinary(zipPath,dir string)(string,error){z,err:=zip.OpenReader(zipPath);if err!=nil{return "",err};defer z.Close();want:="tunnel-client";if runtime.GOOS=="windows"{want+=".exe"};for _,f:=range z.File{if filepath.Base(f.Name)!=want{continue};rc,err:=f.Open();if err!=nil{return "",err};dst:=filepath.Join(dir,want);out,err:=os.OpenFile(dst,os.O_CREATE|os.O_TRUNC|os.O_WRONLY,0700);if err!=nil{rc.Close();return "",err};_,err=io.Copy(out,io.LimitReader(rc,128<<20));rc.Close();out.Close();if err!=nil{return "",err};return dst,nil};return "",errors.New("tunnel-client executable not found in archive")}
func(i *Installer)activePath()string{return filepath.Join(i.Root,"tools","tunnel-client","active.json")}
func(i *Installer)readActive()(Active,error){b,err:=os.ReadFile(i.activePath());if err!=nil{return Active{},err};var a Active;err=json.Unmarshal(b,&a);return a,err}
func(i *Installer)writeActive(a Active)error{if err:=os.MkdirAll(filepath.Dir(i.activePath()),0700);err!=nil{return err};b,_:=json.MarshalIndent(a,"","  ");tmp:=i.activePath()+".tmp";if err:=os.WriteFile(tmp,b,0600);err!=nil{return err};return os.Rename(tmp,i.activePath())}
func(i *Installer)Rollback()(Active,error){cur,_:=i.readActive();root:=filepath.Join(i.Root,"tools","tunnel-client");ents,err:=os.ReadDir(root);if err!=nil{return Active{},err};var vers []string;for _,e:=range ents{if e.IsDir()&&e.Name()!=cur.Version{vers=append(vers,e.Name())}};sort.Sort(sort.Reverse(sort.StringSlice(vers)));want:="tunnel-client";if runtime.GOOS=="windows"{want+=".exe"};for _,v:=range vers{p:=filepath.Join(root,v,want);if _,err:=os.Stat(p);err==nil{a:=Active{Version:v,Path:p};if err:=i.writeActive(a);err!=nil{return Active{},err};return a,nil}};return Active{},errors.New("no previous tunnel-client version available")}
