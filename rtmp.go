package fathomrtmp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// Configuration values that would presumably be passed in when the pod is being created rather than hardcoded
// Only the timeouts were specified by the requirements
const (
	kubeAPIURL           string = "http://localhost:8080"                // URL for Kubernetes API (for testing)
	serverPort           string = "localhost:1935"                       // Port for the RTMP server (localhost is included to appease my firewall)
	instanceId           string = "7263a41b-0b0b-4643-8ed3-ae5c00fcc561" // Unique identifier for the instance
	lifetimeTimeoutHours int    = 6                                      // Timeout for the server's lifetime
	usageTimeoutMinutes  int    = 15                                     // Timeout for server inactivity
)

// Logger to format any information to be logged
// Can be changed to write to a file if needed
var logger = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)

// Signal is an empty struct to pass as signals between functions
// Made it a type in case it needs to be expanded upon later
type Signal struct{}

// KubeEndpointType is a type for defining Kubernetes API endpoints
type KubeEndpointType string

// KubeEndpoint defines the endpoints for the Kubernetes API
var KubeEndpoint = struct {
	UpdateStatus KubeEndpointType
	Notify       KubeEndpointType
	Complete     KubeEndpointType
}{
	UpdateStatus: "/update-status",
	Notify:       "/notify",
	Complete:     "/complete",
}

// ShutdownReasonType is a type for defining reasons for shutting down
type ShutdownReasonType string

// ShutdownReason defines reasons for shutting down the server
var ShutdownReason = struct {
	Usage    ShutdownReasonType
	Lifetime ShutdownReasonType
}{
	Usage:    "usage",
	Lifetime: "lifetime",
}

// InstanceStatus is a custom type for instance status
type instanceStatusType string

// InstanceStatuses defines all possible instance statuses
var InstanceStatus = struct {
	Inactive          instanceStatusType
	Starting          instanceStatusType
	Running           instanceStatusType
	ShutdownRequested instanceStatusType
	ShuttingDown      instanceStatusType
	ShutdownFailed    instanceStatusType
}{
	Inactive:          "Inactive",
	Starting:          "Starting",
	Running:           "Running",
	ShutdownRequested: "ShutdownRequested",
	ShuttingDown:      "ShuttingDown",
	ShutdownFailed:    "ShutdownFailed",
}

// State/status of the instance
var status instanceStatusType = InstanceStatus.Inactive

// Mutex for protecting access to the status
var statusMux sync.Mutex

// Map to keep track of active connections
var connections map[string]net.Conn = make(map[string]net.Conn)

// Mutex for protecting access to the connection map
var connectionsMux sync.Mutex

// HandleStream simulates handling an RTMP stream
// For testing purposes, it just waits (sleeps)
func HandleStream(connection net.Conn) {
	time.Sleep(time.Duration(3) * time.Second)
}

// SendRequest sends a request to a given endpoint with set parameters
func SendRequest(req KubeEndpointType, params url.Values) error {
	// Build the full URL with query parameters
	u, err := url.Parse(kubeAPIURL + string(req))
	if err != nil {
		logger.Printf("Error parsing URL: %s\n", err)
		return err
	}
	u.RawQuery = params.Encode()

	// Create a new POST request with the URL containing query parameters
	request, err := http.NewRequest("POST", u.String(), nil)
	if err != nil {
		logger.Printf("Error creating request: %s\n", err)
		return err
	}

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(request)
	if err != nil {
		logger.Printf("Error sending request: %s\n", err)
		return err
	}
	defer resp.Body.Close()

	// Check the response status
	if resp.StatusCode != http.StatusOK {
		logger.Printf("Request to %s returned status %s\n", u.String(), resp.Status)
	}
	return nil
}

// notifyKubernetes sends a generic request to the Kubernetes notify endpoint to pass on any important information
// Currently only used to notify Kubernetes if an error occurs while shutting down the server
func notifyKubernetes(reason, message string) {
	url := kubeAPIURL + string(KubeEndpoint.Notify)
	payload := map[string]string{
		"reason":  reason,
		"message": message,
	}
	jsonPayload, _ := json.Marshal(payload)
	http.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
}

// startLifetimeTimer sends a signal to shut down the server after the configured lifetime has elapsed
func startLifetimeTimer(shutdownChan chan ShutdownReasonType) {
	time.Sleep(time.Duration(lifetimeTimeoutHours) * time.Hour)
	logger.Printf("Instance has been alive for %d hours.\n", lifetimeTimeoutHours)
	shutdownChan <- ShutdownReason.Lifetime
}

// startUsageTimer sends a signal to shut down the server after the server has gone without a new connection for a configured amount of time
func startUsageTimer(shutdownChan chan ShutdownReasonType, connectionChan chan Signal) {
	timer := time.NewTimer(time.Duration(usageTimeoutMinutes) * time.Minute)
	for {
		select {
		case <-connectionChan:
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(time.Duration(usageTimeoutMinutes) * time.Minute)
		case <-timer.C:
			shutdownChan <- ShutdownReason.Usage
			logger.Printf("Instance has gone %d minutes without receiving a new connection.\n", usageTimeoutMinutes)
			return
		}
	}
}

// updateStatusAndSendRequest updates the status of the instance and notifies Kubernetes of the new status
func updateStatusAndSendRequest(newStatus instanceStatusType, params url.Values) error {
	statusMux.Lock()
	status = newStatus
	if params == nil {
		params = url.Values{}
	}
	params.Add("status", string(newStatus))
	err := SendRequest(KubeEndpoint.UpdateStatus, params)
	if err != nil {
		logger.Printf("Error updating status and sending request: %s\n", err)
	}
	statusMux.Unlock()
	return err
}

// requestShutdown begins the process of shutting down the server by updating the status to ShutdownRequested, and notifies Kubernetes of the request
func requestShutdown(reason ShutdownReasonType) error {
	params := url.Values{}
	params.Add("reason", string(reason))
	err := updateStatusAndSendRequest(InstanceStatus.ShutdownRequested, params)
	return err
}

// connectionComplete notifies Kubernetes that HandleStream has completed on a specific connection by UUID
func connectionComplete(uuid string) error {
	params := url.Values{}
	params.Add("uuid", uuid)
	return SendRequest(KubeEndpoint.Complete, params)
}

// confirmShutdown sets the instance's status to ShuttingDown and notifies Kubernetes
func confirmShutdown() error {
	logger.Printf("Shutting down and notifying Kubernetes.\n")
	return updateStatusAndSendRequest(InstanceStatus.ShuttingDown, nil)
}

// confirmStartup sets the instance's status to Running and notifies Kubernetes
func confirmStartup() error {
	logger.Printf("Confirming startup and notifying Kubernetes.\n")
	return updateStatusAndSendRequest(InstanceStatus.Running, nil)
}

// sendResponse sends a response to a request given a status and body
func sendResponse(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// getStatus writes a response to a request containing the instance's status and number of active stream connections
func getStatus(w http.ResponseWriter, r *http.Request) {
	statusMux.Lock()
	// Create status response
	payload := map[string]string{
		"status":  string(status),
		"streams": fmt.Sprintf("%d", len(connections)),
	}
	statusMux.Unlock()
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		logger.Printf("Error marshalling status into JSON: %s\n", err)
		sendResponse(w, http.StatusInternalServerError, fmt.Sprintf("Error marshalling status into JSON: %s\n", err))
	}
	sendResponse(w, http.StatusOK, jsonPayload)
}

// StartServer creates and starts a server to listen for requests
func startServer() {
	// Create channels for communication between functions
	shutdownServer := make(chan ShutdownReasonType)
	connectionChan := make(chan Signal)

	// For concurrency
	var wg sync.WaitGroup

	// Start timers
	go startLifetimeTimer(shutdownServer)
	go startUsageTimer(shutdownServer, connectionChan)

	// Endpoint handlers
	http.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		handleNewStream(w, r, &wg)
	})
	http.HandleFunc("/status", getStatus)

	// Create server
	server := &http.Server{
		Addr: serverPort,
	}

	// Start the server and log the status of the listener
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Printf("ListenAndServe(): %s\n", err)
		}
	}()

	logger.Println("Server started on", serverPort)
	confirmStartup()

	// Await a signal to shut down from one of the timers
	shutdownReason := <-shutdownServer
	logger.Println("Requesting shutdown.")

	// Notify Kubernetes of intent to shut down, but don't lock out any new streams until after a response is given
	requestShutdown(shutdownReason)
	connectionsMux.Lock()
	if len(connections) > 0 {
		logger.Printf("Waiting on %d streams to complete.\n", len(connections))
	}
	connectionsMux.Unlock()

	// Wait for all current connections to finish before starting the shutdown process
	wg.Wait()
	confirmShutdown()
	logger.Println("Shutting down.")

	// Shut down the server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Printf("Server shutdown failed: %s", err)
		notifyKubernetes("Server Shutdown Failed", err.Error())
	}
	logger.Println("Server exited")
}

// handleNewStream prepares a new connection to send to the stream handler
func handleNewStream(w http.ResponseWriter, r *http.Request, wg *sync.WaitGroup) {
	// Acquire lock on mutex as soon as possible in case a pending shutdown checks for new connections while function is running
	connectionsMux.Lock()

	// Example struct of what might be sent by Kubernetes
	// For sake of this exercise I'm assuming it's simple
	// For added security, could be encrypted and need decoding here
	var streamDetails struct {
		UUID string `json:"id"`
		URL  string `json:"url"`
	}
	// Parse the JSON body
	err := json.NewDecoder(r.Body).Decode(&streamDetails)
	if err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		connectionsMux.Unlock()
		return
	}

	// Create connection object
	conn, err := net.Dial("tcp", streamDetails.URL)
	if err != nil {
		http.Error(w, "Unable to connect to stream", http.StatusInternalServerError)
		connectionsMux.Unlock()
		return
	}

	// Add connection to the map of current connections and release mutex
	connections[streamDetails.UUID] = conn
	connectionsMux.Unlock()

	// Respond to new stream creation
	sendResponse(w, http.StatusOK, nil)

	// Add to the waitgroup and send the connection to HandleStream
	wg.Add(1)
	go func() {
		defer wg.Done()
		HandleStream(conn)
		connectionsMux.Lock()
		delete(connections, conn.RemoteAddr().String())
		err := connectionComplete(streamDetails.UUID)
		if err != nil {
			logger.Printf("Error calling connectionComplete: %s\n", err)
		}
		connectionsMux.Unlock()
	}()
}

// Run starts the server
func Run() {
	startServer()
}
