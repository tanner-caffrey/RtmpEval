# Tanner Caffrey RTMP Implementation

I might have gone a little overboard by implementing so much, but I was having fun and wanted to be sure it worked.  
My code covers creating and starting the service's server, handling its basic endpoints, and the shutdown process.

## Questions for PM / Tech Lead
- What should the names of the service's endpoints be?
- If a stream goes on too long, should/can it be migrated to be handled by another pod if this service has long, long outlasted its lifetime?
- What should the structure and content of log messages be when notifying Kubernetes of status changes or errors?
- Are there any security requirements or authentication mechanisms for the service endpoints?
- Can Kubernetes decide that the service should instead not shut down? In which case, would it give the service set amount of time before it should request to shut down once more?

## Assumptions
- Configurations do not change at runtime
- The entire system is running on a secure intranet with only essential ports exposed
- Kubernetes is configured correctly to route all information passed to it correctly
    - Being notified of logs correctly
    - Load Balancer already keeps track of how many active connections the pod has
- Once Kubernetes is informed that an instance wants to shut down, it will stop sending it streams
- The service operates within predefined resource limits (CPU, memory) set by Kubernetes, and it is assumed that these limits are sufficient for handling the expected load

## Assumptions made for sake of the exercise
- SteamHandler does not fail, handles the stream data correctly, and will always finish
- Any environment variables like port, Kubernetes endpoints, id, etc would in actuality be read from a configuration file or similar; they would not be hardcoded like I have done
- No errors will occur when starting the server
- StreamHandler does not error
- Kubernetes relies on status notifications from the server, as opposed to solely requesting its status
    - This is why the service notifies Kubernetes each time a stream has completed or its status has changed
- Kubernetes assigns a uuid to each stream
- Kubernetes parses requests through params (if there was more information being sent to Kubernetes, it would be in JSON)
- No malicious attempts to access the service would be made - otherwise authentication should be used.
- Once Kubernetes has responded to a request to shut down, it will immediately stop sending streams to the service.
    - If it's possible that streams could still be in transit, I would have a grace period of a configurable amount of time the service should wait after receiving the OK to shut down and actually starting to shut down.

## Additional Changes
- In the provided code, error handling is minimal. In a production environment, comprehensive error handling should be implemented to handle scenarios such as network failures, invalid requests, and resource constraints.
- While a basic logger is set up, consider integrating with a centralized logging system for better traceability and analysis of logs.
- Load testing should be done to verify that the service can handle the expected peak load
- Monitoring and alerting mechanisms should also exist to track the health and performance of the service, including metrics such as active connections, error rates, and response times.
- Comments would also be more fleshed out, including returns and parameters
- I'm not perfect and this code would be reviewed by the team to be improved and tested before implementation :)