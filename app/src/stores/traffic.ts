// Глобальный singleton-store с историей трафика на пиров и интерфейсы.
//
// Раз в POLL_MS дёргает /traffic (один запрос на весь мир), считает
// delta от кумулятивных счётчиков ядра и пушит rate (байты/сек) в
// кольцевой буфер фиксированной длины. Интерфейсный агрегат — сумма
// по его пирам.
//
// Полинг запускается из App.vue при mount и живёт пока приложение открыто.
// Переход между страницами store не сбрасывает — это и даёт «непрерывный»
// график, когда юзер скачет между Overview и страницей интерфейса.

import { reactive } from 'vue';
import { api } from '../api/client';

export const POLL_MS = 2000;         // раз в 2 сек
export const HISTORY_LEN = 60;       // 60 × 2s = 2 минуты истории
const STALE_AFTER_MS = 30_000;       // 30с без тика — графики считаются остановленными

export interface PeerHistory {
  rxRate: number[];                  // байт/сек, длина HISTORY_LEN
  txRate: number[];
  prevRxBytes: number;
  prevTxBytes: number;
  lastHandshake: number;             // unix seconds
  lastTickMs: number;                // Date.now() последнего обновления
  seen: boolean;                     // получили ли хотя бы одну свежую выборку после init
}

export interface InterfaceHistory {
  rxRate: number[];
  txRate: number[];
}

interface TrafficStore {
  peers: Record<string, PeerHistory>;          // ключ — public_key
  interfaces: Record<string, InterfaceHistory>; // ключ — name
  lastTickMs: number;
  running: boolean;
}

export const trafficStore: TrafficStore = reactive({
  peers: {},
  interfaces: {},
  lastTickMs: 0,
  running: false,
});

let timer: ReturnType<typeof setInterval> | null = null;

export function startTrafficPolling(): void {
  if (trafficStore.running) return;
  trafficStore.running = true;
  void tick();
  timer = setInterval(() => void tick(), POLL_MS);
}

export function stopTrafficPolling(): void {
  if (timer) clearInterval(timer);
  timer = null;
  trafficStore.running = false;
}

/** Текущий rate по пиру: последний элемент буфера. 0 если пира ещё нет. */
export function peerRate(pubKey: string): { rx: number; tx: number } {
  const p = trafficStore.peers[pubKey];
  if (!p) return { rx: 0, tx: 0 };
  const i = p.rxRate.length - 1;
  return { rx: p.rxRate[i] ?? 0, tx: p.txRate[i] ?? 0 };
}

/** Были ли пирсом переданы байты недавно (последние N тиков). */
export function peerHasRecentData(pubKey: string, ticks = 3): boolean {
  const p = trafficStore.peers[pubKey];
  if (!p || !p.seen) return false;
  const tail = p.rxRate.slice(-ticks).concat(p.txRate.slice(-ticks));
  return tail.some((v) => v > 0);
}

/** Суммарный текущий rate на интерфейсе. */
export function interfaceRate(name: string): { rx: number; tx: number } {
  const h = trafficStore.interfaces[name];
  if (!h) return { rx: 0, tx: 0 };
  const i = h.rxRate.length - 1;
  return { rx: h.rxRate[i] ?? 0, tx: h.txRate[i] ?? 0 };
}

/** Данные устарели? (polling умер или никогда не был запущен) */
export function isStale(): boolean {
  if (!trafficStore.lastTickMs) return true;
  return Date.now() - trafficStore.lastTickMs > STALE_AFTER_MS;
}

async function tick(): Promise<void> {
  try {
    const entries = await api.traffic();
    const now = Date.now();
    const intervalSec = POLL_MS / 1000;

    // собираем агрегат по интерфейсам за этот тик
    const ifaceAgg: Record<string, { rx: number; tx: number }> = {};

    for (const e of entries) {
      let peer = trafficStore.peers[e.public_key];
      if (!peer) {
        // первый тик по этому пиру — запомнить prev, историю оставить нулями
        peer = {
          rxRate: new Array(HISTORY_LEN).fill(0),
          txRate: new Array(HISTORY_LEN).fill(0),
          prevRxBytes: e.rx_bytes,
          prevTxBytes: e.tx_bytes,
          lastHandshake: e.latest_handshake,
          lastTickMs: now,
          seen: false,
        };
        trafficStore.peers[e.public_key] = peer;
        // не пушим в interface-агрегат, потому что rate пока не посчитан
        continue;
      }

      // counters в ядре монотонны; сброс при рестарте интерфейса → отрицательный delta
      const dRx = Math.max(0, e.rx_bytes - peer.prevRxBytes);
      const dTx = Math.max(0, e.tx_bytes - peer.prevTxBytes);
      const rateRx = dRx / intervalSec;
      const rateTx = dTx / intervalSec;

      peer.rxRate.push(rateRx);
      peer.rxRate.shift();
      peer.txRate.push(rateTx);
      peer.txRate.shift();
      peer.prevRxBytes = e.rx_bytes;
      peer.prevTxBytes = e.tx_bytes;
      peer.lastHandshake = e.latest_handshake;
      peer.lastTickMs = now;
      peer.seen = true;

      ifaceAgg[e.interface] ??= { rx: 0, tx: 0 };
      ifaceAgg[e.interface].rx += rateRx;
      ifaceAgg[e.interface].tx += rateTx;
    }

    for (const [name, rate] of Object.entries(ifaceAgg)) {
      let iface = trafficStore.interfaces[name];
      if (!iface) {
        iface = {
          rxRate: new Array(HISTORY_LEN).fill(0),
          txRate: new Array(HISTORY_LEN).fill(0),
        };
        trafficStore.interfaces[name] = iface;
      }
      iface.rxRate.push(rate.rx);
      iface.rxRate.shift();
      iface.txRate.push(rate.tx);
      iface.txRate.shift();
    }

    trafficStore.lastTickMs = now;
  } catch {
    // одиночный сбой сети — игнорируем; следующий тик догонит
  }
}
