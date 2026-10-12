import http from 'k6/http';
import { check } from 'k6';
import { Trend, Rate } from 'k6/metrics';

const latency = new Trend('usage_ingest_ms');
const failures = new Rate('usage_failures');
export const options = {
  scenarios: { usage: { executor: 'constant-arrival-rate', rate: 50, timeUnit: '1s', duration: '30s', preAllocatedVUs: 20, maxVUs: 100 } },
  thresholds: { usage_ingest_ms: ['p(95)<250'], usage_failures: ['rate<0.01'], http_req_failed: ['rate<0.01'] },
};
export function setup() {
  const res = http.post(`${__ENV.BASE_URL}/v1/auth/login`, JSON.stringify({email:'owner@acme.test',password:'demo-password',tenant:'acme'}), {headers:{'Content-Type':'application/json'}});
  check(res, {'login succeeded': r => r.status === 200});
  return { token: res.json('access_token') };
}
export default function(data) {
  const body = JSON.stringify({idempotency_key:`k6-${__VU}-${__ITER}`,meter:'api_calls',quantity:1,occurred_at:new Date().toISOString(),metadata:{source:'k6'}});
  const res = http.post(`${__ENV.BASE_URL}/v1/usage`, body, {headers:{'Content-Type':'application/json',Authorization:`Bearer ${data.token}`}});
  latency.add(res.timings.duration); failures.add(res.status !== 201); check(res, {'created': r => r.status === 201});
}
