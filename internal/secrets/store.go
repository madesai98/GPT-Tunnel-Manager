package secrets

import(
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)
var ErrNotFound=errors.New("secret not found")
type Store interface{Put(context.Context,string,[]byte)error;Get(context.Context,string)([]byte,error);Delete(context.Context,string)error}
func ValidateRef(ref string)error{if !strings.HasPrefix(ref,"secret://")||len(ref)<=len("secret://"){return errors.New("secret reference must use secret://")};return nil}
func envName(ref string)string{h:=sha256.Sum256([]byte(ref));return "GTM_SECRET_"+strings.ToUpper(hex.EncodeToString(h[:8]))}
type envFallback struct{native Store}
func wrap(native Store)Store{return &envFallback{native:native}}
func(e *envFallback)Put(ctx context.Context,ref string,v []byte)error{if err:=ValidateRef(ref);err!=nil{return err};return e.native.Put(ctx,ref,v)}
func(e *envFallback)Get(ctx context.Context,ref string)([]byte,error){if err:=ValidateRef(ref);err!=nil{return nil,err};if v,ok:=os.LookupEnv(envName(ref));ok{return []byte(v),nil};return e.native.Get(ctx,ref)}
func(e *envFallback)Delete(ctx context.Context,ref string)error{if err:=ValidateRef(ref);err!=nil{return err};return e.native.Delete(ctx,ref)}
