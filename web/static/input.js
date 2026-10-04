'use strict';
// タッチ／マウス操作を判定し、ホストへ送る操作イベントに変換する。
//
//   タップ            → 左クリック
//   ドラッグ          → 左ボタンを押したまま移動
//   長押し（500ms）   → 右クリック
//   2 本指で上下左右  → スクロール（指の動きにコンテンツが追従する向き）
//
// 座標は映像の表示領域（object-fit: contain の黒帯を除いた範囲）に対する 0〜1 で送る。
// Pointer Events を使うので、PC の Chrome / Edge ではマウスで同じ操作を確認できる。

const MonitoInput = (() => {
  const LONG_PRESS_MS = 500;
  const MOVE_THRESHOLD_PX = 10; // これ以上動いたらタップではなくドラッグ
  const WHEEL_PER_PX = 3; // 指の移動 1px あたりのホイール量（40px で 1 ノッチ = 120）

  function attach(video, send) {
    const pointers = new Map(); // pointerId → {x, y}（クライアント座標）
    let mode = 'idle'; // idle | pending | drag | scroll | done
    let start = null; // 最初の指の {x, y}
    let longTimer = null;
    let scrollLast = null; // 2 本指の重心
    let wheelAcc = { v: 0, h: 0 };
    let pendingMove = null; // rAF でまとめて送る移動
    let rafId = 0;

    // 映像の実表示領域に対する正規化座標
    function normalize(cx, cy) {
      const r = video.getBoundingClientRect();
      const vw = video.videoWidth || r.width;
      const vh = video.videoHeight || r.height;
      const s = Math.min(r.width / vw, r.height / vh);
      const w = vw * s;
      const h = vh * s;
      const ox = r.left + (r.width - w) / 2;
      const oy = r.top + (r.height - h) / 2;
      return {
        x: Math.min(1, Math.max(0, (cx - ox) / w)),
        y: Math.min(1, Math.max(0, (cy - oy) / h)),
      };
    }

    function sendAt(t, cx, cy, extra) {
      const p = normalize(cx, cy);
      send(Object.assign({ t, x: p.x, y: p.y }, extra || {}));
    }

    // 移動は描画フレームごとに 1 回へまとめる
    function queueMove(cx, cy) {
      pendingMove = { cx, cy };
      if (!rafId) {
        rafId = requestAnimationFrame(() => {
          rafId = 0;
          if (pendingMove) {
            sendAt('move', pendingMove.cx, pendingMove.cy);
            pendingMove = null;
          }
        });
      }
    }

    function flushMove() {
      if (pendingMove) {
        sendAt('move', pendingMove.cx, pendingMove.cy);
        pendingMove = null;
      }
    }

    function clearLongPress() {
      if (longTimer) {
        clearTimeout(longTimer);
        longTimer = null;
      }
    }

    function centroid() {
      let x = 0;
      let y = 0;
      for (const p of pointers.values()) {
        x += p.x;
        y += p.y;
      }
      return { x: x / pointers.size, y: y / pointers.size };
    }

    function beginScroll() {
      clearLongPress();
      mode = 'scroll';
      scrollLast = centroid();
      wheelAcc = { v: 0, h: 0 };
      // ホイールはカーソル下のウィンドウに届くので、先にカーソルを指の間へ移す
      sendAt('move', scrollLast.x, scrollLast.y);
    }

    function onDown(e) {
      e.preventDefault();
      try {
        video.setPointerCapture(e.pointerId); // 指が映像の外へ出ても追い続ける
      } catch {
        /* 取得できない環境では無視 */
      }
      pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });

      // PC のマウス右ボタンはそのまま右クリック
      if (e.pointerType === 'mouse' && e.button === 2) {
        sendAt('down', e.clientX, e.clientY, { b: 'right' });
        sendAt('up', e.clientX, e.clientY, { b: 'right' });
        mode = 'done';
        return;
      }

      if (pointers.size === 1 && mode === 'idle') {
        mode = 'pending';
        start = { x: e.clientX, y: e.clientY };
        longTimer = setTimeout(() => {
          longTimer = null;
          if (mode === 'pending' && pointers.size === 1) {
            sendAt('down', start.x, start.y, { b: 'right' });
            sendAt('up', start.x, start.y, { b: 'right' });
            mode = 'done'; // 指を離すまで以降の動きは無視
          }
        }, LONG_PRESS_MS);
      } else if (pointers.size === 2 && mode === 'pending') {
        beginScroll();
      }
      // ドラッグ中の 2 本目、3 本目以降の指は無視する
    }

    function onMove(e) {
      if (!pointers.has(e.pointerId)) {
        // マウスのホバー移動（ボタンを押していない）はカーソルだけ動かす
        if (e.pointerType === 'mouse' && mode === 'idle') queueMove(e.clientX, e.clientY);
        return;
      }
      e.preventDefault();
      pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });

      if (mode === 'pending') {
        const dx = e.clientX - start.x;
        const dy = e.clientY - start.y;
        if (Math.hypot(dx, dy) >= MOVE_THRESHOLD_PX) {
          clearLongPress();
          mode = 'drag';
          sendAt('down', start.x, start.y, { b: 'left' });
          queueMove(e.clientX, e.clientY);
        }
      } else if (mode === 'drag') {
        queueMove(e.clientX, e.clientY);
      } else if (mode === 'scroll' && pointers.size >= 2) {
        const c = centroid();
        // 指を上へ動かす（dy<0）とページは下へ進む（ホイールは負）
        wheelAcc.v += (c.y - scrollLast.y) * WHEEL_PER_PX;
        wheelAcc.h += -(c.x - scrollLast.x) * WHEEL_PER_PX;
        scrollLast = c;
        const v = Math.trunc(wheelAcc.v);
        const h = Math.trunc(wheelAcc.h);
        if (v !== 0 || h !== 0) {
          wheelAcc.v -= v;
          wheelAcc.h -= h;
          send({ t: 'wheel', v, h });
        }
      }
    }

    function onUp(e) {
      if (!pointers.has(e.pointerId)) return;
      e.preventDefault();
      pointers.delete(e.pointerId);
      const cancelled = e.type === 'pointercancel';

      if (mode === 'pending' && pointers.size === 0) {
        clearLongPress();
        if (!cancelled) {
          // タップ = 左クリック
          sendAt('down', start.x, start.y, { b: 'left' });
          sendAt('up', start.x, start.y, { b: 'left' });
        }
      } else if (mode === 'drag' && pointers.size === 0) {
        flushMove();
        sendAt('up', e.clientX, e.clientY, { b: 'left' });
      } else if (mode === 'scroll' && pointers.size === 1) {
        // 2 本指のうち 1 本を離したら、残りの指を離すまで何もしない
        mode = 'done';
      }

      if (pointers.size === 0) {
        clearLongPress();
        mode = 'idle';
        start = null;
      }
    }

    video.addEventListener('pointerdown', onDown);
    video.addEventListener('pointermove', onMove);
    video.addEventListener('pointerup', onUp);
    video.addEventListener('pointercancel', onUp);
    video.addEventListener('contextmenu', (e) => e.preventDefault());

    // PC のマウスホイール（動作確認用）。Chrome は 1 ノッチ = deltaY 100 前後
    video.addEventListener(
      'wheel',
      (e) => {
        e.preventDefault();
        const unit = e.deltaMode === 1 ? 40 : 1; // 行単位なら px に換算
        const v = Math.round((-e.deltaY * unit * 120) / 100);
        const h = Math.round((e.deltaX * unit * 120) / 100);
        if (v || h) {
          sendAt('move', e.clientX, e.clientY);
          send({ t: 'wheel', v, h });
        }
      },
      { passive: false }
    );
  }

  return { attach };
})();
