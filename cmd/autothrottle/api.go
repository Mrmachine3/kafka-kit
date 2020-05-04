package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/DataDog/kafka-kit/kafkazk"
)

// APIConfig holds configuration params for the admin API.
type APIConfig struct {
	Listen      string
	ZKPrefix    string
	RateSetting string
}

var (
	overrideRateZnode     = "override_rate"
	overrideRateZnodePath string
	incorrectMethod       = "disallowed method\n"
)

func initAPI(c *APIConfig, zk kafkazk.Handler) {
	c.RateSetting = overrideRateZnode
	overrideRateZnodePath = fmt.Sprintf("/%s/%s", c.ZKPrefix, overrideRateZnode)

	m := http.NewServeMux()

	// Check ZK for override rate config znode.
	exists, err := zk.Exists(overrideRateZnodePath)
	if err != nil {
		log.Fatal(err)
	}

	if !exists {
		// Create chroot.
		err = zk.Create("/"+c.ZKPrefix, "")
		if err != nil {
			log.Fatal(err)
		}
		// Create overrideZKPath.
		err = zk.Create(overrideRateZnodePath, "")
		if err != nil {
			log.Fatal(err)
		}
	}

	// If the znode exists, check if it's using the legacy (non-json) format.
	// If it is, update it to the json format.
	// TODO(jamie): we can probably remove this by now.
	if exists {
		r, _ := zk.Get(overrideRateZnodePath)
		if rate, err := strconv.Atoi(string(r)); err == nil {
			// Populate the updated config.
			err := setThrottleOverride(zk, overrideRateZnodePath, ThrottleOverrideConfig{Rate: rate})
			if err != nil {
				log.Fatal(err)
			}

			log.Println("Throttle override config format updated")
		}
	}

	// Routes. A global rate vs broker-specific rate is distinguished in whether
	// or not there's a trailing slash (and in a properly formed request, the
	// addition of a broker ID in the request path).
	m.HandleFunc("/throttle", func(w http.ResponseWriter, req *http.Request) { throttleGetSet(w, req, zk) })
	m.HandleFunc("/throttle/", func(w http.ResponseWriter, req *http.Request) { throttleGetSet(w, req, zk) })
	m.HandleFunc("/throttle/remove", func(w http.ResponseWriter, req *http.Request) { throttleRemove(w, req, zk) })
	m.HandleFunc("/throttle/remove/", func(w http.ResponseWriter, req *http.Request) { throttleRemove(w, req, zk) })

	// Deprecated routes.
	m.HandleFunc("/get_throttle", func(w http.ResponseWriter, req *http.Request) { getThrottleDeprecated(w, req, zk) })
	m.HandleFunc("/set_throttle", func(w http.ResponseWriter, req *http.Request) { setThrottleDeprecated(w, req, zk) })
	m.HandleFunc("/remove_throttle", func(w http.ResponseWriter, req *http.Request) { removeThrottleDeprecated(w, req, zk) })

	// Start listener.
	go func() {
		err := http.ListenAndServe(c.Listen, m)
		if err != nil {
			log.Fatal(err)
		}
	}()
}

// throttleGetSet conditionally handles the request depending on the HTTP method.
func throttleGetSet(w http.ResponseWriter, req *http.Request, zk kafkazk.Handler) {
	logReq(req)

	switch req.Method {
	case http.MethodGet:
		// Get a throttle rate.
		getThrottle(w, req, zk)
	case http.MethodPost:
		// Set a throttle rate.
		setThrottle(w, req, zk)
	default:
		// Invalid method.
		w.WriteHeader(http.StatusMethodNotAllowed)
		io.WriteString(w, incorrectMethod)
		return
	}
}

// throttleRemove removes either the global or broker-specific throttle.
func throttleRemove(w http.ResponseWriter, req *http.Request, zk kafkazk.Handler) {
	logReq(req)

	switch req.Method {
	case http.MethodPost:
		// Remove the throttle.
		removeThrottle(w, req, zk)
	default:
		// Invalid method.
		w.WriteHeader(http.StatusMethodNotAllowed)
		io.WriteString(w, incorrectMethod)
		return
	}
}

// getThrottle sets a throtle rate that applies to all brokers.
func getThrottle(w http.ResponseWriter, req *http.Request, zk kafkazk.Handler) {
	r, err := getThrottleOverride(zk, overrideRateZnodePath)
	if err != nil {
		io.WriteString(w, err.Error())
		return
	}

	switch r.Rate {
	case 0:
		io.WriteString(w, "no throttle override is set\n")
	default:
		resp := fmt.Sprintf("a throttle override is configured at %dMB/s, autoremove==%v\n",
			r.Rate, r.AutoRemove)
		io.WriteString(w, resp)
	}
}

// setThrottle returns the throttle rate applied to all brokers.
func setThrottle(w http.ResponseWriter, req *http.Request, zk kafkazk.Handler) {
	// Check rate param.
	rate, err := parseRateParam(req)
	if err != nil {
		io.WriteString(w, err.Error())
		return
	}

	// Check autoremove param.
	autoRemove, err := parseAutoRemoveParam(req)
	if err != nil {
		io.WriteString(w, err.Error())
		return
	}

	// Populate configs.
	rateCfg := ThrottleOverrideConfig{
		Rate:       rate,
		AutoRemove: autoRemove,
	}

	// Determine whether this is a global or broker-specific override.
	paths := parsePaths(req)

	// Set the config.
	err = setThrottleOverride(zk, overrideRateZnodePath, rateCfg)
	if err != nil {
		io.WriteString(w, fmt.Sprintf("%s\n", err))
	} else {
		io.WriteString(w, fmt.Sprintf("throttle successfully set to %dMB/s, autoremove==%v\n",
			rate, autoRemove))
	}
}

// removeThrottle removes the throttle rate applied to all brokers.
func removeThrottle(w http.ResponseWriter, req *http.Request, zk kafkazk.Handler) {
	c := ThrottleOverrideConfig{
		Rate:       0,
		AutoRemove: false,
	}

	err := setThrottleOverride(zk, overrideRateZnodePath, c)
	if err != nil {
		io.WriteString(w, fmt.Sprintf("%s\n", err))
	} else {
		io.WriteString(w, "throttle successfully removed\n")
	}
}
