package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Store struct { Root string }
func NewStore(root string)*Store{return &Store{Root:root}}
func(s *Store)ManagerPath()string{return filepath.Join(s.Root,"config","manager.json")}
func(s *Store)ServersPath()string{return filepath.Join(s.Root,"config","servers.json")}
func(s *Store)LoadOrCreate()(ManagerConfig,ServersConfig,error){ if err:=os.MkdirAll(filepath.Join(s.Root,"config"),0700); err!=nil{return ManagerConfig{},ServersConfig{},err}; m:=DefaultManagerConfig(); ss:=DefaultServersConfig(); cm,cs:=false,false; if err:=readJSON(s.ManagerPath(),&m); err!=nil { if !errors.Is(err,os.ErrNotExist){return m,ss,fmt.Errorf("load manager config: %w",err)};cm=true }; if err:=readJSON(s.ServersPath(),&ss); err!=nil { if !errors.Is(err,os.ErrNotExist){return m,ss,fmt.Errorf("load servers config: %w",err)};cs=true }; if err:=ValidateManager(m);err!=nil{return m,ss,err}; if err:=ValidateServers(ss);err!=nil{return m,ss,err}; if cm {if err:=s.SaveManager(m);err!=nil{return m,ss,err}}; if cs {if err:=s.SaveServers(ss);err!=nil{return m,ss,err}}; return m,ss,nil }
func(s *Store)SaveManager(c ManagerConfig)error{if err:=ValidateManager(c);err!=nil{return err};return atomicWriteJSON(s.ManagerPath(),c)}
func(s *Store)SaveServers(c ServersConfig)error{if err:=ValidateServers(c);err!=nil{return err};return atomicWriteJSON(s.ServersPath(),c)}
func readJSON(path string,dst any)error{f,err:=os.Open(path);if err!=nil{return err};defer f.Close();d:=json.NewDecoder(io.LimitReader(f,8<<20));d.DisallowUnknownFields();if err:=d.Decode(dst);err!=nil{return err};var extra any;if err:=d.Decode(&extra);err!=io.EOF{if err==nil{return errors.New("unexpected trailing JSON value")};return err};return nil}
func atomicWriteJSON(path string,v any)error{if err:=os.MkdirAll(filepath.Dir(path),0700);err!=nil{return err};b,err:=json.MarshalIndent(v,"","  ");if err!=nil{return err};b=append(b,'\n');tmp,err:=os.CreateTemp(filepath.Dir(path),".tmp-*.json");if err!=nil{return err};name:=tmp.Name();ok:=false;defer func(){_ = tmp.Close();if !ok{_ = os.Remove(name)}}();if err:=tmp.Chmod(0600);err!=nil{return err};if _,err:=tmp.Write(b);err!=nil{return err};if err:=tmp.Sync();err!=nil{return err};if err:=tmp.Close();err!=nil{return err};if err:=os.Rename(name,path);err!=nil{return err};if d,err:=os.Open(filepath.Dir(path));err==nil{_ = d.Sync();_ = d.Close()};ok=true;return nil}
