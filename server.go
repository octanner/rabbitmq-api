package main

import "github.com/martini-contrib/binding"

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/bitly/go-simplejson"
	"github.com/go-martini/martini"
	_ "github.com/lib/pq"
	"github.com/martini-contrib/render"
	"github.com/nu7hatch/gouuid"
)

type ClusterInfo struct {
	Url      string
        UiUrl    string
	Username string
	Password string
	Amqp     string
	Cluster  string
}

var clusters []ClusterInfo
var key []byte
var brokerdb string

// Init function
// gets credentials from vault, creates database, populates cluster info
func Init() {
	adminuser, adminpass := getvaultcreds()
	uri := brokerdb
	db, err := sql.Open("postgres", uri)
	if err != nil {
		fmt.Println(err)
	}
	defer db.Close()

	//create database if it doesn't exist
	buf, err := ioutil.ReadFile("create.sql")
	if err != nil {
		fmt.Println("Error: Unable to run migration scripts, oculd not load create.sql.")
		os.Exit(1)
	}
	_, err = db.Exec(string(buf))
	if err != nil {
		fmt.Println(err)
		fmt.Println("Error: Unable to run migration scripts, execution failed.")
		os.Exit(1)
	}

	populateClusterInfo(adminuser, adminpass)
}

func main() {
	Init()
	m := martini.Classic()
	m.Use(render.Renderer())
	m.Get("/v1/rabbitmq/plans", plans)
	m.Post("/v1/rabbitmq/instance", binding.Json(provisionspec{}), provision)
	m.Get("/v1/rabbitmq/url/:name", url)
	m.Delete("/v1/rabbitmq/instance/:vhost", Delete)
	m.Post("/v1/tag", binding.Json(tagspec{}), tag)
	m.Run()

}

type provisionspec struct {
	Plan        string `json:"plan"`
	Billingcode string `json:"billingcode"`
}

// func store(...)
// TODO: Document
// TODO: error handling
// TODO: configure SQL dialect
func store(cluster string, vhost string, username string, password string, billingcode string) {
	uri := brokerdb
	db, err := sql.Open("postgres", uri)
	if err != nil {
		fmt.Println(err)
	}
	var newname string
	err = db.QueryRow("INSERT INTO provision(cluster,vhost,username,password_enc,billingcode) VALUES($1,$2,$3,$4,$5) returning username;", cluster, vhost, username, Encrypt(password), billingcode).Scan(&newname)

	if err != nil {
		fmt.Println(err)
	}
	fmt.Println(newname)
	err = db.Close()
}

// func retreive(..)
// TODO: Document
// TODO: error handling
func retreive(v string) (c string, vh string, u string, p string, t []tagspec) {
	uri := brokerdb
	db, err := sql.Open("postgres", uri)
	if err != nil {
		fmt.Println(err)
	}
	stmt, err := db.Prepare("select cluster,vhost,username,password_enc,tags from provision where vhost = $1 ")
	if err != nil {
		fmt.Println(err)
	}
	defer stmt.Close()
	rows, err := stmt.Query(v)
	defer rows.Close()
	var cluster string
	var vhost string
	var username string
	var password_enc string
	var tags []byte
	for rows.Next() {
		err := rows.Scan(&cluster, &vhost, &username, &password_enc, &tags)
		if err != nil {
			fmt.Println(err)
			db.Close()
		}
	}
	var tagsa []tagspec
	json.Unmarshal(tags, &tagsa)
	for _, element := range tagsa {
		fmt.Println(element.Resource)
		fmt.Println(element.Name)
		fmt.Println(element.Value)
	}
	db.Close()
	return cluster, vhost, username, Decrypt(password_enc), tagsa

}

// func Delete(..)
// TODO: Document
// TODO: error handling
func Delete(params martini.Params, r render.Render) {
	err := delete(params["vhost"])
	if err != nil {
		r.JSON(500, err)
	}
	r.JSON(200, nil)
}

// func delete(..)
// TODO: Document
// TODO: error handling
// TODO: rename to avoid conflect with built in function
func delete(vhost string) error {

	cluster, _, _, _, _ := retreive(vhost)

	clusterinfo := clusterInfo(cluster)
	username := clusterinfo.Username
	password := clusterinfo.Password
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	client := &http.Client{}
	request, _ := http.NewRequest("DELETE", "http://"+clusterinfo.Url+":15672/api/vhosts/"+vhost, nil)
	request.Header.Add("Authorization", "Basic "+auth)
	request.Header.Add("Content-type", "application/json")
	response, _ := client.Do(request)
	defer response.Body.Close()
	_, _ = ioutil.ReadAll(response.Body)

	uri := brokerdb
	db, dberr := sql.Open("postgres", uri)
	if dberr != nil {
		fmt.Println(dberr)
		return dberr
	}

	fmt.Println("# Deleting")
	stmt, err := db.Prepare("delete from provision where vhost=$1")
	if err != nil {
		return err
	}
	res, err := stmt.Exec(vhost)
	if err != nil {
		return err
	}
	affect, err := res.RowsAffected()
	if err != nil {
		return err
	}
	fmt.Println(affect, "rows changed")

	return nil
}

// func url(..)
// TODO: Document
// TODO: error handling
func url(params martini.Params, r render.Render) {
	rcluster, rvhost, rusername, rpassword, _ := retreive(params["name"])
	url := clusterInfo(rcluster).Amqp
	amqp := "amqp://" + rusername + ":" + rpassword + "@" + url + ":5672/" + rvhost
	var m map[string]string
	m = make(map[string]string)
	m["RABBITMQ_URL"] = amqp
	m["RABBITMQUI_URL"] = clusterInfo(rcluster).UiUrl
	r.JSON(200, m)
}

// func provision(..)
// TODO: Document
// TODO: error handling
func provision(spec provisionspec, err binding.Errors, r render.Render) {
	cluster := spec.Plan
	billingcode := spec.Billingcode

	newusername, newpassword := createuserandpassword()

	create(cluster, newusername, newusername, newpassword)
	store(cluster, newusername, newusername, newpassword, billingcode)
	rcluster, rvhost, rusername, rpassword, _ := retreive(newusername)
	url := clusterInfo(rcluster).Amqp
	amqp := "amqp://" + rusername + ":" + rpassword + "@" + url + ":5672/" + rvhost
	var m map[string]string
	m = make(map[string]string)
	m["RABBITMQ_URL"] = amqp
	m["RABBITMQUI_URL"] = clusterInfo(rcluster).UiUrl
	r.JSON(201, m)

}

// func createuserandpassword(..)
// TODO: Document
func createuserandpassword() (ur string, pr string) {

	u, _ := uuid.NewV4()
	newusername := "u" + strings.Split(u.String(), "-")[0]
	p, _ := uuid.NewV4()
	newpassword := "p" + strings.Split(p.String(), "-")[0] + strings.Split(p.String(), "-")[1] + strings.Split(p.String(), "-")[2]
	return newusername, newpassword
}

// func create(..)
// TODO: Document
// TODO: error handling
func create(cluster string, vhost string, newusername string, newpassword string) {

	createvhost(cluster, vhost)
	createUser(cluster, newusername, newpassword)
	grantUser(cluster, newusername, vhost)
	grantAdmin(cluster, vhost)
	createMirrorPolicy(cluster, vhost)
	createTestQueue(cluster, vhost, newusername, newpassword)
}

// func clusterInfo(..)
// TODO: Document
// TODO: error handling
func clusterInfo(cluster string) ClusterInfo {
	var clusterinfo ClusterInfo
	//clusterinfo.Url = os.Getenv(strings.ToUpper(cluster) + "_RABBIT_URL")
	//clusterinfo.Username = os.Getenv(strings.ToUpper(cluster) + "_RABBIT_USERNAME")
	//clusterinfo.Password = os.Getenv(strings.ToUpper(cluster) + "_RABBIT_PASSWORD")
	//clusterinfo.Amqp = os.Getenv(strings.ToUpper(cluster) + "_RABBIT_AMQP")
	for _, element := range clusters {
		if element.Cluster == cluster {
			clusterinfo = element
		}
	}
	return clusterinfo
}

// func grantUser(..)
// TODO: Document
// TODO: error handling
func grantUser(cluster string, newusername string, vhost string) {

	type Permissions struct {
		Configure string `json:"configure"`
		Write     string `json:"write"`
		Read      string `json:"read"`
	}
	clusterinfo := clusterInfo(cluster)
	var permissions Permissions
	permissions.Configure = ".*"
	permissions.Write = ".*"
	permissions.Read = ".*"

	str, err := json.Marshal(permissions)
	if err != nil {
		fmt.Println("Error preparing request")
	}
	jsonStr := []byte(string(str))
	username := clusterinfo.Username
	password := clusterinfo.Password
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	client := &http.Client{}
	request, _ := http.NewRequest("PUT", "http://"+clusterinfo.Url+":15672/api/permissions/"+vhost+"/"+newusername, bytes.NewBuffer(jsonStr))
	request.Header.Add("Authorization", "Basic "+auth)
	request.Header.Add("Content-type", "application/json")
	response, _ := client.Do(request)
	defer response.Body.Close()
	_, _ = ioutil.ReadAll(response.Body)

}

// func grantAdmin(..)
// TODO: Document
// TODO: error handling
func grantAdmin(cluster string, vhost string) {

	type Permissions struct {
		Configure string `json:"configure"`
		Write     string `json:"write"`
		Read      string `json:"read"`
	}
	clusterinfo := clusterInfo(cluster)
	newusername := clusterinfo.Username
	var permissions Permissions
	permissions.Configure = ".*"
	permissions.Write = ".*"
	permissions.Read = ".*"

	str, err := json.Marshal(permissions)
	if err != nil {
		fmt.Println("Error preparing request")
	}
	jsonStr := []byte(string(str))
	username := clusterinfo.Username
	password := clusterinfo.Password
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	client := &http.Client{}
	request, _ := http.NewRequest("PUT", "http://"+clusterinfo.Url+":15672/api/permissions/"+vhost+"/"+newusername, bytes.NewBuffer(jsonStr))
	request.Header.Add("Authorization", "Basic "+auth)
	request.Header.Add("Content-type", "application/json")
	response, _ := client.Do(request)
	defer response.Body.Close()
	_, _ = ioutil.ReadAll(response.Body)

}

// func createUser(..)
// TODO: Document
// TODO: error handling
func createUser(cluster string, newusername string, newpassword string) {
	type User struct {
		Password string `json:"password"`
		Tags     string `json:"tags"`
	}
	clusterinfo := clusterInfo(cluster)
	var user User
	user.Password = newpassword
	user.Tags = "management"
	str, err := json.Marshal(user)
	if err != nil {
		fmt.Println("Error preparing request")
	}
	jsonStr := []byte(string(str))
	username := clusterinfo.Username
	password := clusterinfo.Password
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	client := &http.Client{}
	request, _ := http.NewRequest("PUT", "http://"+clusterinfo.Url+":15672/api/users/"+newusername, bytes.NewBuffer(jsonStr))
	request.Header.Add("Authorization", "Basic "+auth)
	request.Header.Add("Content-type", "application/json")
	response, _ := client.Do(request)
	defer response.Body.Close()
	_, _ = ioutil.ReadAll(response.Body)

}

// func createvhost(..)
// TODO: Document
// TODO: error handling
func createvhost(cluster string, vhost string) {
	clusterinfo := clusterInfo(cluster)
	username := clusterinfo.Username
	password := clusterinfo.Password
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	client := &http.Client{}
	request, _ := http.NewRequest("PUT", "http://"+clusterinfo.Url+":15672/api/vhosts/"+vhost, nil)
	request.Header.Add("Authorization", "Basic "+auth)
	request.Header.Add("Content-type", "application/json")
	response, _ := client.Do(request)
	defer response.Body.Close()
	_, _ = ioutil.ReadAll(response.Body)
}

// func createMirrorPolicy(..)
// TODO: Document
// TODO: error handling
func createMirrorPolicy(cluster string, vhost string) {
	clusterinfo := clusterInfo(cluster)
	username := clusterinfo.Username
	password := clusterinfo.Password
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))

	var policyname = "ha-" + vhost
	type PolicyDef struct {
		HAMode     string `json:"ha-mode"`
		HAParams   int    `json:"ha-params"`
		HASyncMode string `json:"ha-sync-mode"`
	}
	type Policy struct {
		Pattern    string    `json:"pattern"`
		Priority   int       `json:"priority"`
		ApplyTo    string    `json:"apply-to"`
		Definition PolicyDef `json:"definition"`
	}

	var policy Policy
	policy.Pattern = ".*"
	policy.Priority = 0
	policy.ApplyTo = "all"
	policy.Definition.HAMode = "exactly"
	policy.Definition.HAParams = 3
	policy.Definition.HASyncMode = "automatic"

	payload, err := json.Marshal(policy)
	if err != nil {
		fmt.Println("Error preparing request")
	}
	_ = (string(payload))

	client := &http.Client{}
	request, _ := http.NewRequest("PUT", "http://"+clusterinfo.Url+":15672/api/policies/"+vhost+"/"+policyname, bytes.NewBuffer(payload))
	request.Header.Add("Authorization", "Basic "+auth)
	request.Header.Add("Content-type", "application/json")
	response, _ := client.Do(request)
	defer response.Body.Close()
	_, _ = ioutil.ReadAll(response.Body)
}

// func createTestQueue(..)
// TODO: Document
// TODO: error handling
func createTestQueue(cluster string, vhost string, newusername string, newpassword string) {

	type Queue struct {
		AutoDelete bool `json:"auto_delete"`
		Durable    bool `json:"durable"`
	}
	var queue Queue
	queue.AutoDelete = false
	queue.Durable = true
	str, err := json.Marshal(queue)
	if err != nil {
		fmt.Println("Error preparing request")
	}
	jsonStr := []byte(string(str))

	clusterinfo := clusterInfo(cluster)
	username := newusername
	password := newpassword
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	client := &http.Client{}
	request, _ := http.NewRequest("PUT", "http://"+clusterinfo.Url+":15672/api/queues/"+vhost+"/"+vhost+"queue", bytes.NewBuffer(jsonStr))
	request.Header.Add("Authorization", "Basic "+auth)
	request.Header.Add("Content-type", "application/json")
	response, _ := client.Do(request)
	defer response.Body.Close()
	_, _ = ioutil.ReadAll(response.Body)

}

// func Encrypt(..)
// TODO: Document
// TODO: error handling
func Encrypt(plaintext string) string {
	text := []byte(plaintext)
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	ciphertext := make([]byte, aes.BlockSize+len(text))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(crand.Reader, iv); err != nil {
		panic(err)
	}
	cfb := cipher.NewCFBEncrypter(block, iv)
	cfb.XORKeyStream(ciphertext[aes.BlockSize:], text)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

// func Decrypt(..)
// TODO: Document
// TODO: error handling
func Decrypt(b64 string) string {
	text, _ := base64.StdEncoding.DecodeString(b64)
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	if len(text) < aes.BlockSize {
		panic("ciphertext too short")
	}
	iv := text[:aes.BlockSize]
	text = text[aes.BlockSize:]
	cfb := cipher.NewCFBDecrypter(block, iv)
	cfb.XORKeyStream(text, text)
	return string(text)
}

// func plans(..)
// TODO: Document
// TODO: Refactor to use configured plans, not hard-coded
func plans(params martini.Params, r render.Render) {
	plans := make(map[string]interface{})
	if strings.Contains(os.Getenv("CLUSTERS"), "sandbox") {
		plans["sandbox"] = "Dev and Testing and QA and Load testing.  May be purged regularly"
	}
	if strings.Contains(os.Getenv("CLUSTERS"), "live") {
		plans["live"] = "Prod and real use. Bigger cluster.  Not purged"
	}
	r.JSON(200, plans)

}

// struct tagspec(..)
// TODO: Document
type tagspec struct {
	Resource string `json:"resource"`
	Name     string `json:"name"`
	Value    string `json:"value"`
}

// func tag(..)
// TODO: Document
// TODO: error handling
func tag(spec tagspec, berr binding.Errors, r render.Render) {
	if berr != nil {
		fmt.Println(berr)
		errorout := make(map[string]interface{})
		errorout["error"] = berr
		r.JSON(500, errorout)
		return
	}
	var tags []tagspec
	_, _, _, _, tags = retreive(spec.Resource)
	tags = append(tags, spec)
	str, err := json.Marshal(tags)
	if err != nil {
		fmt.Println("Error preparing request")
	}
	jsonStr := (string(str))
	uri := brokerdb
	db, err := sql.Open("postgres", uri)
	if err != nil {
		fmt.Println(err)
	}

	var nvhost string
	err = db.QueryRow("UPDATE provision set tags=$1 where vhost=$2 returning vhost;", jsonStr, spec.Resource).Scan(&nvhost)

	if err != nil {
		fmt.Println(err)
	}
	err = db.Close()

	r.JSON(201, map[string]interface{}{"response": "tag added"})

}

// func getvaultcreds(..)
// TODO: Document
// TODO: error handling
func getvaultcreds() (u string, p string) {
	vaulttoken := os.Getenv("VAULT_TOKEN")
	vaultaddr := os.Getenv("VAULT_ADDR")
	rabbitmqsecret := os.Getenv("RABBITMQ_SECRET")
	vaultaddruri := vaultaddr + "/v1/" + rabbitmqsecret
	vreq, err := http.NewRequest("GET", vaultaddruri, nil)
	vreq.Header.Add("X-Vault-Token", vaulttoken)
	vclient := &http.Client{}
	vresp, err := vclient.Do(vreq)
	if err != nil {
		fmt.Println(err)
	}
	defer vresp.Body.Close()
	bodyj, err := simplejson.NewFromReader(vresp.Body)
	if err != nil {
		fmt.Println(err)
	}
	adminusername, _ := bodyj.Get("data").Get("username").String()
	adminpassword, _ := bodyj.Get("data").Get("password").String()
	keystring, _ := bodyj.Get("data").Get("key").String()
	key = []byte(keystring)
	brokerdb, _ = bodyj.Get("data").Get("brokerdb").String()
	return adminusername, adminpassword

}

// func retreive(..)
// TODO: Document
// TODO: Refactor to get dynamic cluster
func populateClusterInfo(adminuser string, adminpass string) {
	/*
	   var sandbox ClusterInfo
	     var live    ClusterInfo

	       adminuser, adminpass := getvaultcreds()
	       sandbox.Url = os.Getenv("SANDBOX_RABBIT_URL")
	       sandbox.Username = adminuser
	       sandbox.Password = adminpass
	       sandbox.Amqp = os.Getenv("SANDBOX_RABBIT_AMQP")
	       sandbox.Cluster = "sandbox"

	       live.Url = os.Getenv("LIVE_RABBIT_URL")
	       live.Username = adminuser
	       live.Password = adminpass
	       live.Amqp = os.Getenv("LIVE_RABBIT_AMQP")
	       live.Cluster = "live"
	       clusters=append(clusters, sandbox)
	       clusters=append(clusters, live)
	*/
	clusterstoload := strings.Split((os.Getenv("CLUSTERS")), ",")
	fmt.Println("Loading clusters: " + strings.Join(clusterstoload, ","))
	for _, element := range clusterstoload {
		var c ClusterInfo
		c.Url = os.Getenv(strings.ToUpper(element) + "_RABBIT_URL")
                c.UiUrl = os.Getenv(strings.ToUpper(element) + "_UI_URL")
		c.Username = adminuser
		c.Password = adminpass
		c.Amqp = os.Getenv(strings.ToUpper(element) + "_RABBIT_AMQP")
		c.Cluster = element
		clusters = append(clusters, c)
	}

}
