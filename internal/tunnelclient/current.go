package tunnelclient
func(i *Installer)Current()(Active,error){return i.readActive()}
