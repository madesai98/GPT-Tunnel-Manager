package marker
import("errors";"fmt";"strings")
const Prefix="GTM_SERVER_ID="
func Generate(id string)string{return fmt.Sprintf("Managed by GPT Tunnel Manager.\n%s%s\nFollow the GPT Tunnel Manager Lifecycle Skill before using this plugin.",Prefix,id)}
func Parse(s string)(string,error){for _,line:=range strings.Split(s,"\n"){line=strings.TrimSpace(line);if strings.HasPrefix(line,Prefix){id:=strings.TrimSpace(strings.TrimPrefix(line,Prefix));if id==""{break};return id,nil}};return "",errors.New("GTM_SERVER_ID marker not found")}
