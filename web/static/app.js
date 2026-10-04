'use strict';
// Moni-to クライアント
// 認証の流れ：
//   1. QR の URL（http://<IP>/?t=トークン）で開く → WebSocket で送ると、端末鍵が返る
//   2. mDNS 名（http://monito-<PC名>.local/）に届けば、そこへ ?s=端末鍵 付きで移動する
//      → 「ホーム画面に追加」すると、IP が変わっても使える URL が起動 URL になる
//   3. 以降は端末鍵で接続（ホストを再起動しても有効。-reset-devices で無効化）
// manifest に start_url を書いていないのは、追加時点の URL（?s=...）を起動 URL にするため。

const video = document.getElementById('screen');
const overlay = document.getElementById('overlay');
const messageEl = document.getElementById('message');
const actionBtn = document.getElementById('action');

const SESSION_KEY = 'monito.session';
let ws = null;
let pc = null;
let actionHandler = null;
let halted = false; // 追い出し・認証エラー後は自動で再接続しない
let homeTried = false; // mDNS 名への移動はページごとに 1 回だけ試す

// localStorage はプライベートブラウズ等で例外になることがあるので包む
const store = {
  get(k) { try { return localStorage.getItem(k); } catch { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch { /* 無視 */ } },
  del(k) { try { localStorage.removeItem(k); } catch { /* 無視 */ } },
};

function show(text, buttonLabel, onClick) {
  overlay.hidden = false;
  messageEl.textContent = text;
  if (buttonLabel) {
    actionBtn.textContent = buttonLabel;
    actionBtn.hidden = false;
    actionHandler = onClick;
  } else {
    actionBtn.hidden = true;
    actionHandler = null;
  }
}

function hide() {
  overlay.hidden = true;
}

actionBtn.addEventListener('click', () => {
  if (actionHandler) actionHandler();
});

function credentials() {
  const q = new URLSearchParams(location.search);
  if (q.get('t')) return { t: q.get('t') };
  const s = q.get('s') || store.get(SESSION_KEY);
  if (s) return { s };
  return null;
}

function cleanup() {
  if (ws) {
    ws.onclose = null;
    ws.onmessage = null;
    ws.close();
    ws = null;
  }
  if (pc) {
    pc.ontrack = null;
    pc.onconnectionstatechange = null;
    pc.close();
    pc = null;
  }
  video.srcObject = null;
}

function connect() {
  cleanup();
  halted = false;
  const cred = credentials();
  if (!cred) {
    show('PC のターミナルに表示されている QR コードを\niPhone のカメラで読み取ってください。');
    return;
  }
  show('接続中…');
  if (cred.s) {
    // 「ホーム画面に追加」で鍵付き URL が起動 URL になるよう、常に ?s= を付けておく
    history.replaceState(null, '', '?s=' + encodeURIComponent(cred.s));
  }

  const url = new URL('ws', location.href);
  url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
  url.search = new URLSearchParams(cred).toString();
  const sock = new WebSocket(url);
  ws = sock;
  let finished = false; // サーバー側で意図的に終了した（エラー・追い出し）

  sock.onmessage = async (ev) => {
    const m = JSON.parse(ev.data);
    switch (m.type) {
      case 'session':
        // トークンは使い切り。以降はセッション鍵を使う
        store.set(SESSION_KEY, m.session);
        history.replaceState(null, '', '?s=' + encodeURIComponent(m.session));
        break;
      case 'ready':
        if (await moveToHome(sock, m.home)) return;
        try {
          await startPeer(sock);
        } catch (e) {
          console.error(e);
          show('接続の準備に失敗しました：' + e.message, '再接続', connect);
        }
        break;
      case 'answer':
        if (pc) await pc.setRemoteDescription({ type: 'answer', sdp: m.sdp });
        break;
      case 'info':
        console.log(`[monito] ${m.encoder} ${m.width}x${m.height}`);
        break;
      case 'kicked':
        finished = true;
        halted = true;
        show(m.message, '再接続', connect);
        break;
      case 'error':
        finished = true;
        halted = true;
        if (m.code === 'auth') {
          history.replaceState(null, '', location.pathname);
          if (cred.t && store.get(SESSION_KEY)) {
            // 古い QR を読んだだけなら、保存済みのセッション鍵で接続し直す
            connect();
            return;
          }
          store.del(SESSION_KEY);
          show(m.message);
        } else {
          show(m.message, '再接続', connect);
        }
        break;
    }
  };
  sock.onclose = () => {
    if (ws !== sock || finished) return;
    show('切断されました。', '再接続', connect);
  };
}

// IP の URL で開いている場合、mDNS 名の URL へ移れるか試す。移動したら true
async function moveToHome(sock, home) {
  if (!home || homeTried || location.origin === home) return false;
  const h = location.hostname;
  if (h === 'localhost' || h === '127.0.0.1') return false; // PC での確認時は移動しない
  homeTried = true;
  const key = new URLSearchParams(location.search).get('s') || store.get(SESSION_KEY);
  if (!key) return false;

  let ok = false;
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), 2000);
  try {
    const res = await fetch(home + '/ping', { cache: 'no-store', signal: ac.signal });
    ok = res.ok;
  } catch {
    ok = false;
  } finally {
    clearTimeout(timer);
  }
  try {
    sock.send(JSON.stringify({ type: 'homeProbe', ok }));
  } catch { /* 無視 */ }
  if (!ok) return false;

  show('接続先を切り替えています…');
  location.replace(home + '/?s=' + encodeURIComponent(key));
  return true;
}

async function startPeer(sock) {
  pc = new RTCPeerConnection({ iceServers: [] }); // LAN 内のみ（STUN/TURN なし）
  const tr = pc.addTransceiver('video', { direction: 'recvonly' });
  // H.264 を優先（ホスト側も H.264 しか出さない）
  if (tr.setCodecPreferences && RTCRtpReceiver.getCapabilities) {
    const caps = RTCRtpReceiver.getCapabilities('video');
    if (caps) {
      const h264 = caps.codecs.filter((c) => c.mimeType.toLowerCase() === 'video/h264');
      if (h264.length) tr.setCodecPreferences(h264);
    }
  }

  pc.ontrack = (ev) => {
    video.srcObject = ev.streams[0] || new MediaStream([ev.track]);
    tryPlay();
  };
  pc.onconnectionstatechange = () => {
    const st = pc && pc.connectionState;
    if (st === 'failed') {
      show('映像の接続に失敗しました。\n同じ Wi-Fi に接続しているか、PC のファイアウォール設定を確認してください。', '再接続', connect);
    }
  };

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  await waitIceGathering(pc, 2000);
  sock.send(JSON.stringify({ type: 'offer', sdp: pc.localDescription.sdp }));
}

// LAN 内のホスト候補だけなので収集はすぐ終わる。念のためタイムアウト付き
function waitIceGathering(peer, timeoutMs) {
  if (peer.iceGatheringState === 'complete') return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, timeoutMs);
    peer.addEventListener('icegatheringstatechange', () => {
      if (peer.iceGatheringState === 'complete') {
        clearTimeout(timer);
        resolve();
      }
    });
  });
}

function tryPlay() {
  const p = video.play();
  if (p && p.catch) {
    p.catch(() => {
      // 自動再生がブロックされた場合はタップで再生する
      show('', 'タップして再生', () => {
        video.play().then(hide).catch((e) => show('再生できませんでした：' + e.message, '再接続', connect));
      });
    });
  }
}

// 最初のフレームが表示されたらオーバーレイを消す
video.addEventListener('playing', hide);

// 画面スリープ等で WebSocket が切れていたら、復帰時に接続し直す
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible' && !halted && (!ws || ws.readyState === WebSocket.CLOSED) && credentials()) {
    connect();
  }
});

connect();
