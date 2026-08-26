package secrets
func New(root string)Store{return wrap(newNative(root))}
