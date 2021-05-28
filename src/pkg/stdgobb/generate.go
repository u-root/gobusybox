//go:generate embedvar -file=../bb/bbmain/cmd/main.go -varname=bbMainSource -p=stdgobb -o=bbmain_src.go
//go:generate embedvar -file=../bb/bbmain/register.go -varname=bbRegisterSource -p=stdgobb -o=bbregister_src.go

package stdgobb
