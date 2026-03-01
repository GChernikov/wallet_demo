// DB ping baseline — measures connection pool + SELECT 1 overhead.
// Use this to establish minimum latency floor before testing write paths.
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    ping: {
      executor: 'constant-arrival-rate',
      rate: 3000,
      timeUnit: '1s',
      duration: '60s',
      preAllocatedVUs: 200,
      maxVUs: 1000,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<100'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export default function () {
  const res = http.get(`${BASE_URL}/diagnostics/db-ping`);
  check(res, { 'status 200': (r) => r.status === 200 });
}
