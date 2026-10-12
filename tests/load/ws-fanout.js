import { WebSocket } from 'k6/websockets';
import { Counter, Trend } from 'k6/metrics';
import exec from 'k6/execution';

const wsURL = __ENV.CHAT_WS_URL || 'wss://chat.localhost/ws';
const vus = Number(__ENV.VUS || 150);
const sendEveryMs = Number(__ENV.SEND_EVERY_MS || 5000);
const sessionMs = Number(__ENV.SESSION_MS || 60000);

export const options = {
  hosts: { 'chat.localhost': '127.0.0.1' },
  scenarios: {
    fanout: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: __ENV.RAMP || '1m', target: vus },
        { duration: __ENV.HOLD || '2m', target: vus },
        { duration: '15s', target: 0 },
      ],
      gracefulRampDown: '5s',
    },
  },
  thresholds: {
    chat_fanout_latency: ['p(95)<500'],
    chat_ws_errors: [`count<${Math.max(5, Math.ceil(vus * 0.01))}`],
    ws_connecting: ['p(95)<1000'],
  },
};

const fanoutLatency = new Trend('chat_fanout_latency', true);
const messagesReceived = new Counter('chat_messages_received');
const wsErrors = new Counter('chat_ws_errors');

export default function () {
  const user = `load-${exec.vu.idInTest}`;
  const ws = new WebSocket(`${wsURL}?username=${encodeURIComponent(user)}`);

  ws.onopen = () => {
    const sender = setInterval(() => {
      ws.send(JSON.stringify({ content: `${user} ${Date.now()}` }));
    }, sendEveryMs);
    setTimeout(() => {
      clearInterval(sender);
      ws.close();
    }, sessionMs);
  };

  ws.onmessage = (event) => {
    messagesReceived.add(1);
    const message = JSON.parse(event.data);
    if (message.username !== user) {
      return;
    }
    const sentAt = Number(String(message.content).split(' ')[1]);
    if (sentAt > 0) {
      fanoutLatency.add(Date.now() - sentAt);
    }
  };

  ws.onerror = () => {
    wsErrors.add(1);
  };
}
