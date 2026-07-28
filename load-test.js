import http from 'k6/http';
import { check, sleep } from 'k6';

// Simulate 50 concurrent users constantly sending requests for 10 seconds
export const options = {
    vus: 50,
    duration: '10s',
};

export default function () {
    // Target the health endpoint (Limit: 5, Refill: 100ms)
    const url = 'http://localhost:8080/api/v1/health';

    // Simulate multiple unique users based on virtual user ID
    const params = {
        headers: {
            'X-User-ID': `user_${__VU}`,
        },
    };

    const res = http.get(url, params);

    // Verify the server responds with either success or expected rate limit block
    check(res, {
        'is status 200 (allowed)': (r) => r.status === 200,
        'is status 429 (limited)': (r) => r.status === 429,
        'has retry-after header when limited': (r) => r.status === 429 ? r.headers['Retry-After'] !== undefined : true,
    });

    // Small delay to prevent network stack exhaustion on the local machine
    sleep(0.01);
}
