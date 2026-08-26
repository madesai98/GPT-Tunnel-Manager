package platform

import(
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)
func OpenURL(ctx context.Context,raw string)error{var c *exec.Cmd;switch runtime.GOOS{case"windows":c=exec.CommandContext(ctx,"rundll32","url.dll,FileProtocolHandler",raw);case"darwin":c=exec.CommandContext(ctx,"open",raw);default:c=exec.CommandContext(ctx,"xdg-open",raw)};return c.Start()}
func OpenFolder(ctx context.Context,path string)error{return OpenURL(ctx,"file://"+filepath.ToSlash(path))}
func SetLaunchAtStartup(ctx context.Context,enabled bool,exe string)error{switch runtime.GOOS{case"windows":key:=`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`;if enabled{return exec.CommandContext(ctx,"reg","add",key,"/v","GPTTunnelManager","/t","REG_SZ","/d",fmt.Sprintf("\"%s\" --no-browser",exe),"/f").Run()};return exec.CommandContext(ctx,"reg","delete",key,"/v","GPTTunnelManager","/f").Run();case"darwin":home,err:=os.UserHomeDir();if err!=nil{return err};dir:=filepath.Join(home,"Library","LaunchAgents");path:=filepath.Join(dir,"com.gpt-tunnel-manager.plist");if !enabled{return removeIfExists(path)};if err:=os.MkdirAll(dir,0700);err!=nil{return err};xml:=fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>com.gpt-tunnel-manager</string><key>ProgramArguments</key><array><string>%s</string><string>--no-browser</string></array><key>RunAtLoad</key><true/></dict></plist>`,escapeXML(exe));return os.WriteFile(path,[]byte(xml),0600);default:home,err:=os.UserHomeDir();if err!=nil{return err};dir:=filepath.Join(home,".config","autostart");path:=filepath.Join(dir,"gpt-tunnel-manager.desktop");if !enabled{return removeIfExists(path)};if err:=os.MkdirAll(dir,0700);err!=nil{return err};body:=fmt.Sprintf("[Desktop Entry]\nType=Application\nName=GPT Tunnel Manager\nExec=%s --no-browser\nTerminal=false\nX-GNOME-Autostart-enabled=true\n",desktopQuote(exe));return os.WriteFile(path,[]byte(body),0600)}}
func LaunchAtStartupEnabled(exe string)(bool,error){switch runtime.GOOS{case"windows":out,err:=exec.Command("reg","query",`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`,"/v","GPTTunnelManager").CombinedOutput();return err==nil&&strings.Contains(string(out),exe),nil;case"darwin":home,_:=os.UserHomeDir();_,err:=os.Stat(filepath.Join(home,"Library","LaunchAgents","com.gpt-tunnel-manager.plist"));return err==nil,nil;default:home,_:=os.UserHomeDir();_,err:=os.Stat(filepath.Join(home,".config","autostart","gpt-tunnel-manager.desktop"));return err==nil,nil}}
func removeIfExists(path string)error{err:=os.Remove(path);if os.IsNotExist(err){return nil};return err}
func escapeXML(s string)string{s=strings.ReplaceAll(s,"&","&amp;");s=strings.ReplaceAll(s,"<","&lt;");s=strings.ReplaceAll(s,">","&gt;");return s}
func desktopQuote(s string)string{return "\""+strings.ReplaceAll(s,"\"","\\\"")+"\""}
