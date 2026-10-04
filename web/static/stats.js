'use strict';
// 統計オーバーレイ：getStats() から受信状況を 1 秒ごとに表示する。
// 表示の切り替えは iPhone では 3 本指タップ、PC では S キー（app.js から toggle() を呼ぶ）。

const MonitoStats = (() => {
  const STORE_KEY = 'monito.stats';

  function create(el, getPC, getInfo) {
    let visible = false;
    let timer = 0;
    let prev = null; // 前回の inbound-rtp

    try {
      visible = localStorage.getItem(STORE_KEY) === '1';
    } catch {
      /* 無視 */
    }

    const fmt = (v, digits = 0, unit = '') => (v == null || !isFinite(v) ? '—' : v.toFixed(digits) + unit);

    async function sample() {
      const pc = getPC();
      if (!pc) {
        el.textContent = '統計：未接続';
        prev = null;
        return;
      }
      let report;
      try {
        report = await pc.getStats();
      } catch {
        return;
      }
      let inb = null;
      let pair = null;
      report.forEach((s) => {
        if (s.type === 'inbound-rtp' && s.kind === 'video') inb = s;
        if (s.type === 'transport' && s.selectedCandidatePairId) pair = report.get(s.selectedCandidatePairId);
      });
      if (!pair) {
        report.forEach((s) => {
          if (s.type === 'candidate-pair' && s.nominated && s.state === 'succeeded') pair = s;
        });
      }
      if (!inb) {
        el.textContent = '統計：映像を待っています…';
        return;
      }

      // 前回との差分から 1 秒間の値を出す
      const d = {};
      if (prev && inb.timestamp > prev.timestamp) {
        const dt = (inb.timestamp - prev.timestamp) / 1000;
        const dFrames = (inb.framesDecoded || 0) - (prev.framesDecoded || 0);
        d.fps = dFrames / dt;
        d.kbps = (((inb.bytesReceived || 0) - (prev.bytesReceived || 0)) * 8) / dt / 1000;
        const dEmit = (inb.jitterBufferEmittedCount || 0) - (prev.jitterBufferEmittedCount || 0);
        if (dEmit > 0) d.jb = (((inb.jitterBufferDelay || 0) - (prev.jitterBufferDelay || 0)) / dEmit) * 1000;
        if (dFrames > 0 && inb.totalDecodeTime != null) {
          d.decode = (((inb.totalDecodeTime || 0) - (prev.totalDecodeTime || 0)) / dFrames) * 1000;
        }
        const lost = (inb.packetsLost || 0) - (prev.packetsLost || 0);
        const recv = (inb.packetsReceived || 0) - (prev.packetsReceived || 0);
        d.loss = lost + recv > 0 ? (Math.max(0, lost) / (lost + recv)) * 100 : 0;
      }
      prev = inb;

      const info = getInfo() || {};
      const rtt = pair && pair.currentRoundTripTime != null ? pair.currentRoundTripTime * 1000 : null;
      // 受信側で分かる範囲の遅延（ネットワーク片道 + ジッタバッファ + デコード）。キャプチャ・エンコードは含まない
      const est = rtt != null && d.jb != null ? rtt / 2 + d.jb + (d.decode || 0) : null;

      el.textContent = [
        `解像度   ${inb.frameWidth || '?'}×${inb.frameHeight || '?'}  ${fmt(d.fps, 0)} fps` + (info.fps ? ` / ${info.fps}` : ''),
        `ビットレート ${fmt(d.kbps / 1000, 1, ' Mbps')}` + (info.bitrate ? `（目標 ${(info.bitrate / 1000).toFixed(1)}）` : ''),
        `ジッタバッファ ${fmt(d.jb, 1, ' ms')}  デコード ${fmt(d.decode, 1, ' ms')}`,
        `RTT ${fmt(rtt, 1, ' ms')}  受信側の遅延 ≈ ${fmt(est, 0, ' ms')}`,
        `パケットロス ${fmt(d.loss, 2, '%')}（累計 ${inb.packetsLost || 0}）NACK ${inb.nackCount || 0} PLI ${inb.pliCount || 0}`,
        `フリーズ ${inb.freezeCount || 0} 回`,
        `エンコーダ ${info.encoder || '?'}` + (inb.decoderImplementation ? ` / デコーダ ${inb.decoderImplementation}` : ''),
      ].join('\n');
    }

    function apply() {
      el.hidden = !visible;
      clearInterval(timer);
      timer = 0;
      if (visible) {
        prev = null;
        sample();
        timer = setInterval(sample, 1000);
      }
    }

    function toggle() {
      visible = !visible;
      try {
        localStorage.setItem(STORE_KEY, visible ? '1' : '0');
      } catch {
        /* 無視 */
      }
      apply();
    }

    apply();
    return { toggle };
  }

  return { create };
})();
