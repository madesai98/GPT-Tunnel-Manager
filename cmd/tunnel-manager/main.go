package main

import(
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/madesai98/GPT-Tunnel-Manager/internal/app"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/config"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/instance"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/marker"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/platform"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/portable"
	"github.com/madesai98/GPT-Tunnel-Manager/internal/secrets"
)
const version="1.0.0"
func main(){if err:=run(os.Args[1:]);err!=nil{fmt.Fprintln(os.Stderr,"error:",err);os.Exit(1)}}
func run(args []string)error{exe,err:=os.Executable();if err!=nil{return err};root,err:=portable.Resolve(exe);if err!=nil{return err};if len(args)>0{switch args[0]{case"version","--version","-version":fmt.Println(version);return nil;case"print-root":fmt.Println(root);return nil;case"init":if err:=portable.EnsureWritable(root);err!=nil{return err};_,_,err=config.NewStore(root).LoadOrCreate();return err;case"validate":_,_,err=config.NewStore(root).LoadOrCreate();if err==nil{fmt.Println("configuration valid")};return err;case"marker":if len(args)!=2{return errors.New("usage: tunnel-manager marker <server-id>")};fmt.Println(marker.Generate(args[1]));return nil;case"secret":return secretCommand(root,args[1:])}}
	fs:=flag.NewFlagSet("serve",flag.ContinueOnError);rootFlag:=fs.String("root","","override Portable Root (development/testing)");noBrowser:=fs.Bool("no-browser",false,"do not open the local manager UI");if err:=fs.Parse(args);err!=nil{return err};if *rootFlag!=""{root=*rootFlag};if err:=portable.EnsureWritable(root);err!=nil{return err};owner,err:=instance.Acquire(root);if err!=nil{if errors.Is(err,instance.ErrAlreadyRunning){url:=instance.ExistingAdminURL(root);if url!=""{_ = platform.OpenURL(context.Background(),url);fmt.Println("GPT Tunnel Manager is already running at",url);return nil}};return err};defer func(){c,cancel:=context.WithTimeout(context.Background(),2*time.Second);_ = owner.Close(c);cancel()}();a,err:=app.New(root,exe);if err!=nil{return err};if err:=a.Start();err!=nil{return err};owner.SetAdminURL(a.AdminURL());cfg:=a.ManagerConfig();if !*noBrowser&&!cfg.General.StartMinimized{_ = platform.OpenURL(context.Background(),a.AdminURL())};fmt.Println("GPT Tunnel Manager:",a.AdminURL());sig:=make(chan os.Signal,2);signal.Notify(sig,os.Interrupt,syscall.SIGTERM);select{case<-sig:a.RequestShutdown();case<-a.Done():};<-a.Done();return nil}
func secretCommand(root string,args []string)error{if len(args)<2{return errors.New("usage: tunnel-manager secret <put|delete|get> <secret://ref>")};store:=secrets.New(root);ctx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel();switch args[0]{case"put":r:=bufio.NewReader(io.LimitReader(os.Stdin,1<<20));b,err:=io.ReadAll(r);if err!=nil{return err};b=[]byte(strings.TrimRight(string(b),"\r\n"));return store.Put(ctx,args[1],b);case"delete":return store.Delete(ctx,args[1]);case"get":b,err:=store.Get(ctx,args[1]);if err!=nil{return err};_,err=os.Stdout.Write(append(b,'\n'));return err;default:return errors.New("usage: tunnel-manager secret <put|delete|get> <secret://ref>")}}
