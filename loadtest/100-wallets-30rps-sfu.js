// Scenario B — SELECT FOR UPDATE, high contention
// 100 wallets × 30 RPS = 3000 RPS total
// Run: make seed first to generate wallets-sfu-100.json
import http from 'k6/http';
import { check } from 'k6';
import { SharedArray } from 'k6/data';

const wallets = new SharedArray('wallets', () => JSON.parse(open('./wallets-sfu-100.json')));

export const options = {
  scenarios: {
    sfu_100: {
      executor: 'constant-arrival-rate',
      rate: 3000,
      timeUnit: '1s',
      duration: '60s',
      preAllocatedVUs: 3000,
      maxVUs: 8000,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
    http_req_duration: ['p(95)<2000'],
  },
};

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';

export default function () {
  const walletUUID = wallets[(__VU - 1) % wallets.length];
  const amount = Math.random() > 0.5 ? 100 : -100;

  const res = http.post(
    `${BASE_URL}/balances/update/select-for-update`,
    JSON.stringify({ wallet_uuid: walletUUID, amount: amount }),
    { headers: { 'Content-Type': 'application/json' } }
  );

  check(res, { 'status 200': (r) => r.status === 200 });
}
