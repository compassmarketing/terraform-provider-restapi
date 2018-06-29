package restapi

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/signer/v4"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type api_client struct {
	http_client           *http.Client
	uri                   string
	insecure              bool
	username              string
	password              string
	auth_header           string
	redirects             int
	timeout               int
	id_attribute          string
	copy_keys             []string
	write_returns_object  bool
	create_returns_object bool
	debug                 bool
}

// Make a new api client for RESTful calls
func NewAPIClient(i_uri string, i_insecure bool, i_username string, i_password string, i_auth_header string, i_timeout int, i_id_attribute string, i_copy_keys []string, i_wro bool, i_cro bool, i_debug bool) *api_client {
	if i_debug {
		log.Printf("api_client.go: Constructing debug api_client\n")
	}

	/* Sane default */
	if i_id_attribute == "" {
		i_id_attribute = "id"
	}

	/* Remove any trailing slashes since we will append
	   to this URL with our own root-prefixed location */
	if strings.HasSuffix(i_uri, "/") {
		i_uri = i_uri[:len(i_uri)-1]
	}

	/* Disable TLS verification if requested */
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: i_insecure},
	}

	client := api_client{
		http_client: &http.Client{
			Timeout:   time.Second * time.Duration(i_timeout),
			Transport: tr,
		},
		uri:                   i_uri,
		insecure:              i_insecure,
		username:              i_username,
		password:              i_password,
		auth_header:           i_auth_header,
		id_attribute:          i_id_attribute,
		copy_keys:             i_copy_keys,
		write_returns_object:  i_wro,
		create_returns_object: i_cro,
		redirects:             5,
		debug:                 i_debug,
	}
	return &client
}

/* Helper function that handles sending/receiving and handling
   of HTTP data in and out.
   TODO: Handle redirects */
func (client *api_client) send_request(method string, path string, data string) (string, error) {
	full_uri := client.uri + path
	var req *http.Request
	var err error

	if client.debug {
		log.Printf("api_client.go: method='%s', path='%s', full uri (derived)='%s', data='%s'\n", method, path, full_uri, data)
	}

	buffer := bytes.NewReader([]byte(data))

	if data == "" {
		req, err = http.NewRequest(method, full_uri, nil)
	} else {
		req, err = http.NewRequest(method, full_uri, buffer)

		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	if err != nil {
		log.Fatal(err)
		return "", err
	}

	if client.debug {
		log.Printf("api_client.go: Sending HTTP request to %s...\n", req.URL)
	}

	/* Allow for tokens or other pre-created secrets */
	if client.auth_header != "" {
		req.Header.Set("Authorization", client.auth_header)
	} else if client.username != "" && client.password != "" {
		/* ... and fall back to basic auth if configured */
		req.SetBasicAuth(client.username, client.password)
	}

	if client.debug {
		log.Printf("api_client.go: Request headers:\n")
		for name, headers := range req.Header {
			for _, h := range headers {
				log.Printf("api_client.go:   %v: %v", name, h)
			}
		}

		log.Printf("api_client.go: BODY:\n")
		body := "<none>"
		if req.Body != nil {
			body = string(data)
		}
		log.Printf("%s\n", body)
	}

	/* Add drench-specific account header */
	req.Header.Set("x-drench-account", os.Getenv("DRENCH_ACCOUNT"))

	/* Sign request for aws api gateway */
	_, err = v4.NewSigner(credentials.NewSharedCredentials("", "")).Sign( // searches default paths when passed empty strings
		req, buffer, "execute-api", "us-east-1", time.Now()) //FIXME make region and service dynamic
	if err != nil {
		return "", err
	}

	for num_redirects := client.redirects; num_redirects >= 0; num_redirects-- {
		resp, err := client.http_client.Do(req)

		if err != nil {
			//log.Printf("api_client.go: Error detected: %s\n", err)
			return "", err
		}

		if client.debug {
			log.Printf("api_client.go: Response code: %d\n", resp.StatusCode)
			log.Printf("api_client.go: Response headers:\n")
			for name, headers := range resp.Header {
				for _, h := range headers {
					log.Printf("api_client.go:   %v: %v", name, h)
				}
			}
		}

		bodyBytes, err2 := ioutil.ReadAll(resp.Body)
		resp.Body.Close()

		if err2 != nil {
			return "", err2
		}
		body := string(bodyBytes)

		if resp.StatusCode == 301 || resp.StatusCode == 302 {
			//Redirecting... decrement num_redirects and proceed to the next loop
			//uri = URI.parse(rsp['Location'])
		} else if resp.StatusCode == 404 || resp.StatusCode < 200 || resp.StatusCode >= 303 {
			return "", errors.New(fmt.Sprintf("Unexpected response code '%d': %s", resp.StatusCode, body))
		} else {
			if client.debug {
				log.Printf("api_client.go: BODY:\n%s\n", body)
			}
			return body, nil
		}

	} //End loop through redirect attempts

	return "", errors.New("Error - too many redirects!")
}
