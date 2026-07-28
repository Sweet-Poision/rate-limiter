import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';

// Custom metrics for terminal output clarity
export const errorRate = new Rate('errors_5xx');
export const rateLimited = new Rate('rate_limited_429');
export const successRate = new Rate('success_200');

export const options = {
    // Traffic profile: Ramp up -> Sustain -> Spike -> Cooldown
    stages: [
        { duration: '10s', target: 100 }, // Ramp-up to 100 VUs
        { duration: '15s', target: 100 }, // Sustain normal traffic
        { duration: '10s', target: 800 }, // Traffic spike (Stress point)
        { duration: '10s', target: 0 },   // Ramp-down
    ],
    thresholds: {
        http_req_duration: ['p(95)<50'], // 95% of requests must resolve under 50ms
    },
};

// Target a mix of high-limit (status), low-limit (models/rag), and standard endpoints
const endpoints = [
    '/api/v1/models/metrics/list',
    '/api/v1/auth/profile/read',
    '/api/v1/users/status/read',
    '/api/v2/rag/logs/create',
    '/api/v2/payments/keys/delete'
];

export default function () {
    // Generate high cardinality keys to stress Redis memory and Lua script execution
    const randomUser = Math.floor(Math.random() * 10000);
    const randomEndpoint = endpoints[Math.floor(Math.random() * endpoints.length)];

    const params = {
        headers: {
            'X-User-Id': `test_user_${randomUser}`,
        },
    };

    // Execute request as fast as the network allows (no sleep)
    const res = http.get(`http://localhost:8080${randomEndpoint}`, params);

    check(res, {
        'status is 200': (r) => r.status === 200,
        'status is 429': (r) => r.status === 429,
    });

    // Record custom metrics
    if (res.status === 200) successRate.add(1);
    else if (res.status === 429) rateLimited.add(1);
    else errorRate.add(1);
}
