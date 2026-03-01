import { check } from 'k6';
import http from 'k6/http';

// 100K RPS (100,000 requests per second) configured for maximum EC2 throughput
export const options = {
    scenarios: {
        constant_request_rate: {
            executor: 'constant-arrival-rate',
            rate: 100000, 
            timeUnit: '1s', // 100k requests per second
            duration: '1m', // Sustain for 1 minute
            preAllocatedVUs: 5000, // Pre-allocate 5,000 virtual users
            maxVUs: 20000, // Allow up to 20,000 virtual users if latency spikes
        },
    },
    thresholds: {
        http_req_failed: ['rate<0.01'],   // Error rate must be < 1%
        http_req_duration: ['p(95)<500', 'p(99)<2000'], // 99% of requests < 2s
    },
};

// Ensure API Gateway URL is provided
const BASE_URL = __ENV.GATEWAY_URL || 'http://localhost:8080';

// Multi-tenant testing: Array of different function IDs to invoke randomly
const FUNCTION_IDS = [
    __ENV.FUNCTION_ID_1,
    __ENV.FUNCTION_ID_2,
    __ENV.FUNCTION_ID_3
].filter(Boolean); // Clean any undefined IDs if user didn't provide 3

export default function () {
    if (FUNCTION_IDS.length === 0) {
        console.error("No FUNCTION_ID environment variables provided!");
        return;
    }

    // Pick a random function from the array to test multi-tenancy
    const randomFunctionId = FUNCTION_IDS[Math.floor(Math.random() * FUNCTION_IDS.length)];
    
    // Construct the payload based on the runtime
    const payload = JSON.stringify({
        name: "Mini-Lambda 100K Bench",
        payload: {
            is_load_test: true,
            timestamp: new Date().toISOString()
        }
    });

    const params = {
        headers: {
            'Content-Type': 'application/json',
        },
    };

    // Fire the invocation request
    const res = http.post(`${BASE_URL}/invoke/${randomFunctionId}`, payload, params);

    // Validate the response
    check(res, {
        'status is 200': (r) => r.status === 200,
        'rate limit not hit (status != 429)': (r) => r.status !== 429,
    });
}
