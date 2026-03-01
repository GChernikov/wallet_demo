// HTTP baseline — no database interaction.
// Use this to measure pure HTTP overhead of the server.
import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    noop: {
      executor: 'constant-arrival-rate',
      rate: 3000,
      timeUnit: '1s',
      duration: '60s',
      preAllocatedVUs: 100,
      maxVUs: 500,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<50'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export default function () {
  const res = http.get(`${BASE_URL}/diagnostics/noop`);
  check(res, { 'status 200': (r) => r.status === 200 });
}
