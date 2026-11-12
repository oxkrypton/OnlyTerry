    (function () {
      'use strict';
      var FALLBACK = 'zh-CN';
      var STORE_KEY = 'tg-archive-lang';
      var THEME_KEY = 'tg-archive-theme';
      var SUB_HIDE_KEY = 'tg-archive-subscribe-dismissed';
      var PAGE_SIZE = 30;
      var state = {
        cfg: null, data: null, lang: FALLBACK, failed: null,
        session: 'session3', query: '', limit: PAGE_SIZE, sort: 'desc',
        tlMode: 'month', calMonth: '', calSelected: ''
      };
      var view = { posts: [], rendered: 0, lastMonth: '' };
      var themeReady = false;

      /* Tailwind 类名 + daisyUI 组件类集中管理，避免散落的自定义 CSS */
      var LANG_BASE = 'btn btn-xs btn-ghost font-medium focus:outline-none focus:ring-2 focus:ring-blue-500';
      var LANG_ACTIVE = 'btn-active !bg-white !text-blue-600 dark:!bg-slate-700 dark:!text-blue-400';
      var LANG_IDLE = 'text-slate-600 dark:text-slate-300';
      var TAB_BASE = 'tab font-medium focus:outline-none focus:ring-2 focus:ring-blue-500';
      var TAB_ACTIVE = 'tab-active !bg-blue-600 !text-white';
      var TAB_IDLE = 'text-slate-600 dark:text-slate-300';
      var CLS_EMPTY = 'alert bg-slate-100 dark:bg-slate-800 text-sm text-slate-500 dark:text-slate-400';
      /* 引用块的伪元素竖条 / 单行省略号需要自定义 CSS，统一放在 <style> 的 .tg-quote 段落；
         这里只保留 Tailwind 能表达的卡片 / 正文 / 链接 / 图片类名。 */

      /* Post Card：不使用 daisyUI chat-bubble。气泡左下角的小凸角是 ::before 伪元素，
         不参与父级 box-shadow，所以那一个角没有阴影；改成四角统一的 rounded-xl + shadow-sm 卡片后消失。 */
      var CLS_CARD = 'bg-white dark:bg-slate-800 rounded-xl border border-slate-200/80 dark:border-slate-700 p-4 mb-3 shadow-sm hover:border-slate-300 transition-all';
      var CLS_POST_DATE = 'text-xs font-mono text-slate-400';
      var CLS_TEXT = 'text-sm text-slate-700 dark:text-slate-200 leading-relaxed mt-1.5 break-words whitespace-pre-wrap';
      /* 正文 URL 必须是普通 inline + 强制断词：inline-flex 是不可拆分的 inline 盒，
         长 URL 会整块撑破卡片；附件链接保留 inline-flex，让 mt-3 生效 */
      var CLS_LINK = 'text-blue-600 hover:underline font-medium';
      var CLS_LINK_TEXT = CLS_LINK + ' inline break-all';
      var CLS_IMG = 'mt-3 rounded-lg max-h-60 w-auto object-cover border border-slate-100 cursor-zoom-in max-w-full';
      var CLS_COPY_BTN = 'shrink-0 -mt-1 -mr-1 p-1.5 rounded-md text-slate-400 hover:text-slate-600 hover:bg-slate-100 dark:hover:text-slate-200 dark:hover:bg-slate-700 focus:outline-none focus:ring-2 focus:ring-blue-500 transition-colors';

      /* session → 单栏切换标题，文案走 i18n；默认落在美股 session3 */
      var SESSION_ORDER = ['session2', 'session3'];
      var SESSION_TITLE = { session2: 'sessionCrypto', session3: 'sessionStocks' };
      function sessionTitle(key) { return SESSION_TITLE[key] ? t(SESSION_TITLE[key]) : key; }

      /* Lucide Icons (ISC License) — 全部内联，零额外依赖 */
      var ICONS = {
        sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/>' +
          '<path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/>' +
          '<path d="M2 12h2"/><path d="M20 12h2"/>' +
          '<path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>',
        moon: '<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>',
        copy: '<rect x="8" y="8" width="14" height="14" rx="2" ry="2"/>' +
          '<path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/>',
        check: '<path d="M20 6 9 17l-5-5"/>',
        x: '<path d="M18 6 6 18"/><path d="m6 6 12 12"/>',
        bell: '<path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/>',
        arrowUp: '<path d="M12 19V5"/><path d="m5 12 7-7 7 7"/>',
        info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>'
      };
      function iconSvg(name) {
        return '<svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
          'stroke-width="2" stroke-linecap="round" stroke-linejoin="round" ' +
          'aria-hidden="true">' + ICONS[name] + '</svg>';
      }

      function el(tag, cls, text) {
        var n = document.createElement(tag);
        if (cls) n.className = cls;
        if (text != null) n.textContent = String(text);
        return n;
      }
      function get(id) { return document.getElementById(id); }
      function locales() { return (state.cfg && state.cfg.locales) || ['zh-CN', 'zh-TW', 'en']; }
      function valid(l, cfg) {
        return !!cfg && !!cfg.strings && !!cfg.strings[l] &&
          (cfg.locales || []).indexOf(l) >= 0;
      }
      function pick(pref, cfg) {
        if (valid(pref, cfg)) return pref;
        var d = (cfg && cfg.default) || FALLBACK;
        return valid(d, cfg) ? d : (cfg && cfg.locales ? cfg.locales[0] : FALLBACK);
      }
      function t(key, vars) {
        var pack = (state.cfg && state.cfg.strings && state.cfg.strings[state.lang]) || {};
        var s = pack[key];
        if (s == null && state.cfg && state.cfg.strings) {
          s = (state.cfg.strings[FALLBACK] || {})[key];
        }
        if (s == null) s = key;
        if (vars) Object.keys(vars).forEach(function (k) {
          s = s.split('{' + k + '}').join(String(vars[k]));
        });
        return s;
      }
      function readSaved() { try { return localStorage.getItem(STORE_KEY); } catch (e) { return null; } }
      function save(l) { try { localStorage.setItem(STORE_KEY, l); } catch (e) { } }
      function detect(cfg) {
        var saved = readSaved();
        if (valid(saved, cfg)) return saved;
        var nav = String(navigator.language || navigator.userLanguage || '').toLowerCase();
        if (/hant|-(tw|hk|mo)\b/.test(nav)) return pick('zh-TW', cfg);
        if (/^zh/.test(nav)) return pick('zh-CN', cfg);
        if (/^en/.test(nav)) return pick('en', cfg);
        return pick(null, cfg);
      }

      /* ---------- 主题：data-theme 与 Tailwind dark class 同步 ---------- */
      function readTheme() { try { return localStorage.getItem(THEME_KEY); } catch (e) { return null; } }
      function saveTheme(v) { try { localStorage.setItem(THEME_KEY, v); } catch (e) { } }
      function systemDark() {
        return !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
      }
      function currentTheme() {
        var s = readTheme();
        return (s === 'light' || s === 'dark') ? s : (systemDark() ? 'dark' : 'light');
      }
      function isDark() { return document.documentElement.getAttribute('data-theme') === 'dark'; }
      function updateThemeLabel() {
        var btn = get('themeBtn');
        var dark = isDark();
        var label = state.cfg ? t(dark ? 'themeToLight' : 'themeToDark')
          : (dark ? 'Switch to light theme' : 'Switch to dark theme');
        btn.setAttribute('aria-label', label);
        btn.setAttribute('title', label);
        btn.setAttribute('aria-pressed', dark ? 'true' : 'false');
      }
      function applyTheme(value) {
        document.documentElement.setAttribute('data-theme', value);
        document.documentElement.classList.toggle('dark', value === 'dark');
        var btn = get('themeBtn');
        var name = value === 'dark' ? 'sun' : 'moon';  // 图标表示「点一下会切到的模式」
        btn.setAttribute('data-lucide', name);
        btn.innerHTML = iconSvg(name);
        var mc = document.querySelector('meta[name="theme-color"]');
        if (mc) mc.setAttribute('content', value === 'dark' ? '#0f172a' : '#f8fafc');
        updateThemeLabel();
      }
      function initThemeButton() {
        if (themeReady) { updateThemeLabel(); return; }
        themeReady = true;
        get('themeBtn').addEventListener('click', function () {
          var next = isDark() ? 'light' : 'dark';
          saveTheme(next);
          applyTheme(next);
        });
        applyTheme(currentTheme());
        if (window.matchMedia) {
          var mq = window.matchMedia('(prefers-color-scheme: dark)');
          var onChange = function () { if (!readTheme() && state.cfg) applyTheme(currentTheme()); };
          if (mq.addEventListener) mq.addEventListener('change', onChange);
          else if (mq.addListener) mq.addListener(onChange);
        }
      }

      // 时间本地化只影响格式，不改内容
      function intlFmt(d, lang, wallClock) {
        var locale = (state.cfg && state.cfg.intl && state.cfg.intl[lang]) || lang;
        var opts = { dateStyle: 'medium', timeStyle: 'short', hour12: false };
        opts.timeZone = wallClock ? 'UTC' : 'Asia/Shanghai';  // 更新时刻固定按 UTC+8 展示
        try { return new Intl.DateTimeFormat(locale, opts).format(d); }
        catch (e) { return d.toISOString().slice(0, 16).replace('T', ' '); }
      }
      function fmtPostDate(iso, lang) {
        if (!iso) return '';
        var m = /^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})/.exec(iso);
        var d = m ? new Date(Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5])) : new Date(iso);
        if (isNaN(d)) return String(iso).slice(0, 19).replace('T', ' ');
        return intlFmt(d, lang, true);
      }
      function fmtUpdated(iso, lang) {
        if (!iso) return '';
        var d = new Date(iso);
        if (isNaN(d)) return String(iso).slice(0, 16).replace('T', ' ');
        return intlFmt(d, lang, false);
      }

      function initLangButtons() {
        var box = get('lang');
        box.textContent = '';
        var shortNames = (state.cfg && state.cfg.shortNames) || {};
        var names = (state.cfg && state.cfg.names) || {};
        locales().forEach(function (code) {
          var b = el('button', LANG_BASE + ' ' + LANG_IDLE, shortNames[code] || code);
          b.type = 'button';
          b.setAttribute('data-lang', code);
          b.setAttribute('aria-label', names[code] || code);
          b.setAttribute('title', names[code] || code);
          b.setAttribute('aria-pressed', 'false');
          b.addEventListener('click', function () { setLang(code); });
          box.appendChild(b);
        });
      }
      function updateLangButtons() {
        var box = get('lang');
        box.setAttribute('aria-label', t('langLabel'));
        Array.prototype.forEach.call(box.children, function (b) {
          var on = b.getAttribute('data-lang') === state.lang;
          b.className = LANG_BASE + ' ' + (on ? LANG_ACTIVE : LANG_IDLE);
          b.setAttribute('aria-pressed', on ? 'true' : 'false');
        });
      }

      function sessionKeys() {
        var seen = {};
        ((state.data && state.data.posts) || []).forEach(function (p) {
          seen[p.session || 'session3'] = 1;
        });
        return SESSION_ORDER.filter(function (k) { return seen[k]; }).concat(
          Object.keys(seen).filter(function (k) { return SESSION_ORDER.indexOf(k) < 0; }).sort()
        );
      }
      function allPosts() { return (state.data && state.data.posts) || []; }
      function countBySession() {
        var c = {};
        allPosts().forEach(function (p) { var k = p.session || 'session3'; c[k] = (c[k] || 0) + 1; });
        return c;
      }
      function monthOf(p) {
        if (p.month) return String(p.month).slice(0, 7);
        var s = String(p.date_local || p.date || '');
        return /^\d{4}-\d{2}/.test(s) ? s.slice(0, 7) : '';
      }
      function monthLabel(m) {
        var a = String(m).split('-');
        return t('monthLabel', { year: a[0] || '', month: String(+a[1] || 0) });
      }
      function fmtNum(n) { return Number(n || 0).toLocaleString('en-US'); }

      /* ---------- 侧栏：统计 / 分组 / 月份目录 / 订阅 ---------- */
      function renderStats() {
        var labels = { posts: 'statPosts', sessions: 'statSessions', months: 'statMonths', media: 'statMedia' };
        Object.keys(labels).forEach(function (k) {
          get('stat-' + k + '-label').textContent = t(labels[k]);
        });
        var ps = allPosts(), months = {}, media = 0;
        ps.forEach(function (p) {
          var m = monthOf(p); if (m) months[m] = 1;
          if (p.media && p.media !== 'None' && p.media !== 'null') media++;
        });
        get('stat-posts').textContent = state.data ? fmtNum(state.data.count || ps.length) : '—';
        get('stat-sessions').textContent = state.data ? fmtNum(sessionKeys().length) : '—';
        get('stat-months').textContent = state.data ? t('monthUnit', { count: Object.keys(months).length }) : '—';
        get('stat-media').textContent = state.data ? fmtNum(media) : '—';
      }
      function renderSessionTabs() {
        var box = get('sessions');
        box.textContent = '';
        box.setAttribute('aria-label', t('sessionLabel'));
        get('sessions-title').textContent = t('sessionNav');
        var counts = countBySession(), keys = sessionKeys(), total = 0;
        keys.forEach(function (k) { total += counts[k] || 0; });
        keys.forEach(function (key) {
          var on = key === state.session;
          var b = el('button', 'tg-nav-item' + (on ? ' is-active' : ''));
          b.type = 'button';
          b.setAttribute('data-session', key);
          b.setAttribute('aria-pressed', on ? 'true' : 'false');
          b.appendChild(el('span', '', sessionTitle(key)));
          b.appendChild(el('span', 'tg-nav-count', fmtNum(counts[key])));
          b.addEventListener('click', function () { setSession(key); });
          box.appendChild(b);
        });
        get('session-bar').style.width = (total ? Math.round((counts[state.session] || 0) / total * 100) : 0) + '%';
      }
      function setSession(key) {
        state.session = key;
        state.limit = PAGE_SIZE;
        renderSessionTabs();
        renderList();
      }
      function renderMonthNav() {
        var box = get('month-nav');
        var keepTop = box.scrollTop;   // 重建后恢复，切语言不会把月份目录滚回顶部
        box.textContent = '';
        get('timeline-title').textContent = t('timelineTitle');
        renderTimelineMode();
        var counts = {};
        var ps = navPosts();
        ps.forEach(function (p) {
          var m = monthOf(p); if (m) counts[m] = (counts[m] || 0) + 1;
        });
        var keys = Object.keys(counts).sort().reverse();
        if (!keys.length) { box.appendChild(el('p', 'tg-muted', t('timelineEmpty'))); return; }
        keys.forEach(function (m) {
          var b = el('button', 'tg-month-btn');
          b.type = 'button';
          b.setAttribute('data-month', m);
          b.appendChild(el('span', '', monthLabel(m)));
          b.appendChild(el('span', 'tg-month-count', fmtNum(counts[m])));
          b.addEventListener('click', function () { jumpToMonth(m); });
          box.appendChild(b);
        });
        box.scrollTop = keepTop;
      }
      function highlightMonth(m) {
        Array.prototype.forEach.call(document.querySelectorAll('#month-nav .tg-month-btn'), function (b) {
          b.classList.toggle('is-active', b.getAttribute('data-month') === m);
        });
      }
      /* ---------- 时间轴跳转：只认「实时视口位置」，不认文档坐标 ----------
         旧实现用 offsetTop 累加出的文档坐标或一次性坐标快照，两个前提在这里都不成立：
           1) 月份标题是 position:sticky，滚过后 offsetTop / getBoundingClientRect 会随滚动漂移，
              往上跳时算出的目标≈当前位置，于是「点了没反应」；
           2) 列表分批渲染 + 图片懒加载会持续撑高文档，滚动途中算好的坐标立刻过期，
              往下跳时落到上一个月。
         重构：改用「非 sticky 的消息卡片」作锚点，每帧读取它与视口顶部的实时差值并按比例
         收敛；方向由差值正负自然决定，布局中途怎么变都能自动补偿，上下跳转同一套逻辑。 */
      var anchorRAF = 0, anchorDeadline = 0, anchorNode = null;
      function stopAnchorScroll() {
        if (anchorRAF) { cancelAnimationFrame(anchorRAF); anchorRAF = 0; }
        anchorNode = null;
      }
      function scrollToPost(node) {
        stopAnchorScroll();
        if (!node) return;
        anchorNode = node;
        anchorDeadline = performance.now() + 2600;
        var OFFSET = 48;   // 避让常驻顶部的 sticky 月份标题（top 8px，高约 28px）
        function step() {
          anchorRAF = 0;
          if (!anchorNode || !anchorNode.isConnected) return;
          var delta = anchorNode.getBoundingClientRect().top - OFFSET;
          if (Math.abs(delta) > 1) window.scrollBy(0, delta * 0.24);
          // 收敛后仍继续观察一段时间：懒加载图片撑高布局时立即补回来
          if (performance.now() < anchorDeadline) anchorRAF = requestAnimationFrame(step);
        }
        anchorRAF = requestAnimationFrame(step);
      }
      function firstPostIndex(match) {
        for (var i = 0; i < view.posts.length; i++) if (match(view.posts[i])) return i;
        return -1;
      }
      function ensurePostRendered(idx) {
        state.limit = Math.max(state.limit, idx + PAGE_SIZE);
        if (idx >= view.rendered) appendUntil(state.limit);
        return get('post-' + idx);
      }
      function jumpToMonth(m) {
        var idx = firstPostIndex(function (p) { return monthOf(p) === m; });
        if (idx < 0) return;
        var card = ensurePostRendered(idx);
        if (!card) return;
        highlightMonth(m);
        scrollToPost(card);
      }
      /* ---------- 时间轴目录：按天跳转（日历视图） ----------
         列表按月分批渲染，卡片带 post-<索引> id；日历只统计当前筛选结果的日期，
         点某天直接滚到该天第一条消息，不新增列表里的日期锚点，避免改动消息流布局。 */
      function dayOf(p) {
        var s = String(p.date_local || p.date || '');
        return /^\d{4}-\d{2}-\d{2}/.test(s) ? s.slice(0, 10) : '';
      }
      function pad2(n) { return (n < 10 ? '0' : '') + n; }
      function navPosts() {
        return state.data ? view.posts : allPosts().filter(function (p) {
          return (p.session || 'session3') === state.session;
        });
      }
      function calMonths() {
        var seen = {};
        navPosts().forEach(function (p) { var m = monthOf(p); if (m) seen[m] = 1; });
        return Object.keys(seen).sort().reverse();   // 最新在前
      }
      function setTimelineMode(mode) {
        state.tlMode = mode;
        renderTimelineMode();
        if (mode === 'month') updateMonthHighlight();
      }
      function renderTimelineMode() {
        var day = state.tlMode === 'day';
        get('month-nav').classList.toggle('tg-is-hidden', day);
        get('day-nav').classList.toggle('tg-is-hidden', !day);
        get('timeline-mode').setAttribute('aria-label', t('timelineModeLabel'));
        [['tl-mode-month', !day, 'timelineModeMonthAria'],
         ['tl-mode-day', day, 'timelineModeDayAria']].forEach(function (x) {
          var b = get(x[0]);
          b.classList.toggle('is-active', x[1]);
          b.setAttribute('aria-pressed', x[1] ? 'true' : 'false');
          b.setAttribute('aria-label', t(x[2]));
          b.setAttribute('title', t(x[2]));
        });
        if (day) renderDayNav();
      }
      function renderDayNav() {
        var counts = {};
        navPosts().forEach(function (p) {
          var d = dayOf(p); if (d) counts[d] = (counts[d] || 0) + 1;
        });
        var ms = calMonths();
        var week = get('cal-week'), grid = get('cal-grid');
        var title = get('cal-title'), hint = get('cal-hint');
        var prev = get('cal-prev'), next = get('cal-next');
        prev.setAttribute('aria-label', t('calPrev'));
        next.setAttribute('aria-label', t('calNext'));
        if (!ms.length) {
          title.textContent = ''; week.textContent = ''; grid.textContent = '';
          hint.textContent = t('calEmpty');
          prev.disabled = true; next.disabled = true;
          return;
        }
        if (ms.indexOf(state.calMonth) < 0) state.calMonth = ms[0];
        var m = state.calMonth, y = +m.slice(0, 4), mo = +m.slice(5, 7);
        title.textContent = monthLabel(m);
        var at = ms.indexOf(m);
        prev.disabled = at >= ms.length - 1;   // ‹ 更旧的月份
        next.disabled = at <= 0;               // › 更新的月份

        week.textContent = '';
        t('calWeekdays').split(',').forEach(function (w) {
          week.appendChild(el('span', 'tg-cal-weekday', w));
        });

        var first = new Date(y, mo - 1, 1).getDay();       // 0 = 周日
        var days = new Date(y, mo, 0).getDate();
        var prevDays = new Date(y, mo - 1, 0).getDate();
        // 固定 6 周：各月首日 / 天数不同，5 行与 6 行来回切会让面板高度抖
        var cells = 42;
        grid.textContent = '';
        for (var c = 0; c < cells; c++) {
          var num, inMonth;
          if (c < first) { num = prevDays - first + 1 + c; inMonth = false; }
          else if (c < first + days) { num = c - first + 1; inMonth = true; }
          else { num = c - first - days + 1; inMonth = false; }
          var d = inMonth ? m + '-' + pad2(num) : '';
          var n = d ? (counts[d] || 0) : 0;
          var node;
          if (n) {
            node = el('button', 'tg-cal-cell has-post' + (d === state.calSelected ? ' is-active' : ''), num);
            node.type = 'button';
            node.setAttribute('data-day', d);
            node.setAttribute('aria-label', t('calDayLabel', { date: d, count: n }));
            node.setAttribute('title', t('calDayLabel', { date: d, count: n }));
            node.addEventListener('click', function () {
              jumpToDay(this.getAttribute('data-day'));
            });
          } else {
            node = el('span', 'tg-cal-cell ' + (inMonth ? 'is-disabled' : 'is-outside'), num);
          }
          grid.appendChild(node);
        }
        hint.textContent = '';
      }
      function shiftCalMonth(step) {
        var ms = calMonths(), at = ms.indexOf(state.calMonth);
        if (at < 0 || !ms[at + step]) return;
        state.calMonth = ms[at + step];
        state.calSelected = '';
        renderDayNav();
      }
      function jumpToDay(d) {
        var idx = firstPostIndex(function (p) { return dayOf(p) === d; });
        if (idx < 0) return;
        var card = ensurePostRendered(idx);
        if (!card) return;
        state.calSelected = d;
        renderDayNav();
        highlightMonth(d.slice(0, 7));
        scrollToPost(card);
      }
      function updateMonthHighlight() {
        var heads = document.querySelectorAll('#list .tg-month-head[data-month]');
        if (!heads.length) return;
        var active = heads[0].getAttribute('data-month');
        for (var i = 0; i < heads.length; i++) {
          // head 本身 sticky，rect.top 恒为 8px 不可用；改用其后的第一张消息卡片（非 sticky）
          var first = heads[i].nextElementSibling;
          if (first && first.getBoundingClientRect().top <= 140) active = heads[i].getAttribute('data-month');
          else break;
        }
        highlightMonth(active);
      }
      function renderToolbar() {
        var keys = sessionKeys();
        var key = keys.indexOf(state.session) >= 0 ? state.session : keys[0];
        get('toolbar-title').textContent = sessionTitle(key);
        get('toolbar-count').textContent = state.query.trim()
          ? t('resultHits', { hits: view.posts.length })
          : t('resultTotal', { count: view.posts.length });
        var btn = get('sortBtn');
        btn.textContent = t(state.sort === 'desc' ? 'sortNew' : 'sortOld');
        btn.setAttribute('aria-label', t('sortToggle'));
      }
      function renderSubscribe() {
        get('subscribe-title').textContent = t('subscribeTitle');
        get('subscribe-desc').textContent = t('subscribeDesc');
        get('subscribe-desc2').textContent = t('subscribeDesc2');
        get('subscribe-email').placeholder = t('subscribePlaceholder');
        get('subscribe-email').setAttribute('aria-label', t('subscribePlaceholder'));
        get('subscribe-btn').textContent = t('subscribeBtn');
        get('subscribeClose').setAttribute('aria-label', t('subscribeClose'));
        get('subscribeBell').setAttribute('aria-label', t('subscribeBell'));
        get('backTop').setAttribute('aria-label', t('backTop'));
        var dismissed = false;
        try {
          dismissed = localStorage.getItem(SUB_HIDE_KEY) === '1';
        } catch (e) { }
        get('subscribe-card').classList.toggle('tg-dismissed', dismissed);
        /* 提示已弹出时，切换语言要同步换成当前语言的「暂未开放」 */
        var fb = get('subscribe-feedback');
        if (!fb.classList.contains('tg-is-hidden')) fb.textContent = t('subscribeSoon');
      }

      /* ---------- 关于本站弹窗：文案全走 i18n，切换语言时随 renderChrome 一起刷新 ---------- */
      var ABOUT_TEXT = [
        ['about-title', 'aboutTitle'],
        ['about-lead', 'aboutLead'],
        ['about-data-title', 'aboutDataTitle'],
        ['about-data-body', 'aboutDataBody'],
        ['about-purpose-title', 'aboutPurposeTitle'],
        ['about-purpose-body', 'aboutPurposeBody'],
        ['about-author-title', 'aboutAuthorTitle'],
        ['about-author-body', 'aboutAuthorBody'],
        ['about-next-title', 'aboutNextTitle'],
        ['about-next-body', 'aboutNextBody']
      ];
      function renderAbout() {
        ABOUT_TEXT.forEach(function (pair) { get(pair[0]).textContent = t(pair[1]); });
        get('aboutBtn').setAttribute('aria-label', t('aboutBtn'));
        get('aboutClose').setAttribute('aria-label', t('aboutClose'));
        get('aboutModal').setAttribute('aria-label', t('aboutTitle'));
      }
      function openAbout() {
        var m = get('aboutModal');
        m.classList.remove('hidden');
        m.classList.add('flex');
        m.focus({ preventScroll: true });
      }
      function closeAbout() {
        var m = get('aboutModal');
        if (m.classList.contains('hidden')) return;
        m.classList.add('hidden');
        m.classList.remove('flex');
        if (m.contains(document.activeElement)) get('aboutBtn').focus({ preventScroll: true });
      }

      function setStatus(text) {
        var status = get('status');
        if (text == null) {
          status.hidden = true;
          status.classList.add('hidden');
          return;
        }
        status.hidden = false;
        status.classList.remove('hidden');
        status.textContent = text;
      }
      function renderChrome() {
        /* 不改 <html lang>：正文是简体原文，整页语言固定 zh-CN；
           改动它会让系统按 lang 重选 CJK 回退字体，正文「跟着变字体」 */
        document.title = 'Terry · Message Archive';
        if (state.data) {
          /* 两段各自 nowrap：更新日期 + 时间 + 时区始终同一层，不会被折到下一行 */
          get('meta-count').textContent =
            t('metaCount', { count: state.data.count || 0 }) + t('metaSep');
          get('meta-updated').textContent =
            t('metaUpdated', { date: fmtUpdated(state.data.updated_at, state.lang) });
        } else {
          get('meta-count').textContent = '';
          get('meta-updated').textContent = '';
        }
        get('search-input').placeholder = t('searchPlaceholder');
        get('search-input').setAttribute('aria-label', t('searchPlaceholder'));
        get('header-avatar').alt = t('avatarAlt');
        updateLangButtons();
        renderStats();
        /* 月份目录统一由 renderList() 重建，避免一次切换重建两遍、滚动位置被重置 */
        renderSessionTabs();
        renderToolbar();
        renderSubscribe();
        renderAbout();
        /* 联系按钮展示邮箱地址本身，三语文案只作无障碍提示 */
        get('contact-link').setAttribute('aria-label', t('contactLabel'));
        get('contact-link').setAttribute('title', t('contactLabel'));
        get('join-label').textContent = t('joinLabel');
        updateThemeLabel();
      }

      /* ---------- Post Card 辅助：链接化 / 复制 / 图片放大 ---------- */
      var URL_RE = /(https?:\/\/[^\s<>"']+)/g;
      var URL_TAIL = /[.,;:!?、。，；：！？)\]}】》]+$/;

      /* 正文来自抓取的外部数据，只用 DOM API 拼装，绝不拼 innerHTML */
      function appendRichText(parent, raw) {
        raw = String(raw);
        URL_RE.lastIndex = 0;
        var last = 0, m;
        while ((m = URL_RE.exec(raw)) !== null) {
          if (m.index > last) parent.appendChild(document.createTextNode(raw.slice(last, m.index)));
          var url = m[0], tail = '', hit;
          while ((hit = URL_TAIL.exec(url))) {
            tail = hit[0] + tail;
            url = url.slice(0, -hit[0].length);
          }
          if (url) {
            var a = el('a', CLS_LINK_TEXT, url);
            a.href = url; a.target = '_blank'; a.rel = 'noopener noreferrer';
            parent.appendChild(a);
          }
          if (tail) parent.appendChild(document.createTextNode(tail));
          last = m.index + m[0].length;
        }
        if (last < raw.length) parent.appendChild(document.createTextNode(raw.slice(last)));
      }

      function legacyCopy(text) {
        var ta = document.createElement('textarea');
        ta.value = text;
        ta.setAttribute('readonly', '');
        ta.style.cssText = 'position:fixed;top:-1000px;opacity:0';
        document.body.appendChild(ta);
        ta.select();
        var ok = false;
        try { ok = document.execCommand('copy'); } catch (e) { }
        document.body.removeChild(ta);
        return ok;
      }
      function copyText(text, cb) {
        if (navigator.clipboard && window.isSecureContext) {
          navigator.clipboard.writeText(text).then(
            function () { cb(true); },
            function () { cb(legacyCopy(text)); }
          );
        } else {
          cb(legacyCopy(text));
        }
      }
      function copyButton(text) {
        var btn = el('button', CLS_COPY_BTN);
        btn.type = 'button';
        btn.innerHTML = iconSvg('copy');   // 静态图标，非用户内容
        function label(key) {
          var s = t(key);
          btn.setAttribute('aria-label', s);
          btn.setAttribute('title', s);
        }
        label('copyPost');
        btn.addEventListener('click', function () {
          copyText(text, function (ok) {
            btn.innerHTML = iconSvg(ok ? 'check' : 'copy');
            label(ok ? 'copyDone' : 'copyFailed');
            clearTimeout(btn._copyTimer);
            btn._copyTimer = setTimeout(function () {
              btn.innerHTML = iconSvg('copy');
              label('copyPost');
            }, 1600);
          });
        });
        return btn;
      }

      /* 放大层不碰 body/html 的任何布局属性：锁背景滚动会让滚动条消失、视口变宽，
         部分浏览器还会重置滚动位置，两者都表现为「点开图片抖一下」。
         背景滚动改由 lightbox 上的 wheel/touchmove 阻止，页面本身零改动。 */
      function openLightbox(src, alt) {
        var lb = get('lightbox');
        var sel = window.getSelection();
        if (sel && sel.removeAllRanges) sel.removeAllRanges();  // 清掉点击图片时残留的选中范围
        get('lightboxImg').src = src;
        get('lightboxImg').alt = alt || '';
        lb.setAttribute('aria-label', t('lightboxClose'));
        get('lightboxClose').setAttribute('aria-label', t('lightboxClose'));
        lb.classList.remove('hidden');
        lb.classList.add('flex');
        lb.focus({ preventScroll: true });   // 聚焦全屏容器，不给按钮留蓝色 focus 环
      }
      function closeLightbox() {
        var lb = get('lightbox');
        if (lb.classList.contains('hidden')) return;
        lb.classList.add('hidden');
        lb.classList.remove('flex');
        get('lightboxImg').removeAttribute('src');
      }

      /* 引用块：页面上一律不展示发言者数字 ID。引用群友显示「群友 / Group member」，
         引用博主自己的旧消息直接显示 Terry（专有名词，不走 i18n），配色与 Telegram 回复气泡一致。 */
      function renderQuote(ctx) {
        var missing = !!ctx.missing;
        var q = el('blockquote', 'tg-quote' + (missing ? ' is-missing' : ''));
        var sender = ctx.is_self ? 'Terry' : t('anonSender');
        q.setAttribute('aria-label', t('quoteAria', { sender: sender }));
        q.appendChild(el('span', 'tg-quote-name', sender));

        // 新数据可能带博主选中的引用片段；历史数据没有，直接展示完整原文
        var short = ctx.quote || ctx.text || '';
        var full = (ctx.quote && ctx.text && ctx.quote !== ctx.text) ? ctx.text : short;
        var body = el('div', 'tg-quote-text');
        if (short) {
          body.textContent = short;   // 原文，不做链接化，保持引用块干净
        } else {
          body.classList.add('is-placeholder');
          body.textContent = missing ? t('quoteMissing')
            : ctx.media_type ? t(ctx.media_type === 'photo' ? 'quotedPhoto' : 'quotedMedia')
              : t('nonText');
        }
        q.appendChild(body);
        q._short = short;
        q._full = full;
        return q;
      }

      /* 测量必须在 appendChild 之后：DOM 未入文档时 scrollWidth 恒为 0。
         被截断（或另有完整原文）的引用块整块可点击/回车展开，默认态不出现任何按钮。 */
      function initQuoteExpand(box) {
        Array.prototype.forEach.call(box.querySelectorAll('.tg-quote-text'), function (body) {
          var q = body.parentNode;
          var needMore = q._full !== q._short;
          var clipped = body.scrollWidth > body.clientWidth + 1;
          if (!needMore && (!clipped || body.classList.contains('is-placeholder'))) return;

          q.classList.add('is-expandable');
          q.tabIndex = 0;
          q.setAttribute('aria-expanded', 'false');
          q.setAttribute('title', t('quoteExpand'));
          q.appendChild(el('span', 'tg-quote-hint', t('quoteCollapse')));

          function toggle() {
            var open = q.classList.toggle('is-open');
            body.textContent = open ? q._full : q._short;
            q.setAttribute('aria-expanded', open ? 'true' : 'false');
            q.setAttribute('title', t(open ? 'quoteCollapse' : 'quoteExpand'));
          }
          q.addEventListener('click', toggle);
          q.addEventListener('keydown', function (e) {
            if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); toggle(); }
          });
        });
      }

      function renderPost(p) {
        var card = el('div', CLS_CARD);

        var top = el('div', 'flex items-start justify-between gap-2');
        var time = el('time', CLS_POST_DATE, fmtPostDate(p.date_local || p.date, state.lang));
        time.setAttribute('datetime', p.date_local || p.date || '');
        top.appendChild(time);
        top.appendChild(copyButton(p.text || ''));   // 仅复制正文原文
        card.appendChild(top);

        if (p.reply_context) {
          card.appendChild(renderQuote(p.reply_context));
        }

        if (p.text) {
          var body = el('div', CLS_TEXT);
          appendRichText(body, p.text);  // 原文 + URL 链接化
          card.appendChild(body);
        }

        var rel = p.media;
        if (rel) {
          var name = rel.split('/').pop();
          var url = './media/' + encodeURIComponent(name);
          if (p.media_type === 'photo' && /\.(jpg|jpeg|png|webp|gif)$/i.test(name)) {
            var box = el('div', 'max-w-full');
            var img = el('img', CLS_IMG);
            img.src = url; img.loading = 'lazy'; img.alt = t('imageAlt');
            img.draggable = false;
            // 阻止 mousedown 默认行为，浏览器才不会把点击当成「开始选择/拖拽」而给图片盖蓝层
            img.addEventListener('mousedown', function (e) { e.preventDefault(); });
            img.addEventListener('click', function (e) {
              e.preventDefault();
              openLightbox(img.src, img.alt);
            });
            box.appendChild(img);
            card.appendChild(box);
          } else {
            var a = el('a', CLS_LINK + ' inline-flex items-center gap-0.5 mt-3', t('attachment', { name: name }));
            a.href = url; a.target = '_blank'; a.rel = 'noopener noreferrer';
            card.appendChild(a);
          }
        }
        return card;
      }

      /* ---------- 消息流：分批渲染 ----------
         renderPost() / 消息卡片本体一字未改；这里只控制「渲染几条、何时追加、月份锚点」。 */
      function appendUntil(n) {
        var box = get('list');
        var frag = document.createDocumentFragment();
        var cards = [];
        var target = Math.min(n, view.posts.length);
        var monthCounts = {};
        view.posts.forEach(function (p) {
          var m = monthOf(p); if (m) monthCounts[m] = (monthCounts[m] || 0) + 1;
        });
        while (view.rendered < target) {
          var p = view.posts[view.rendered];
          var m = monthOf(p);
          if (m && m !== view.lastMonth) {
            var head = el('div', 'tg-month-head');
            head.id = 'month-' + m;
            head.setAttribute('data-month', m);
            head.appendChild(el('span', '', monthLabel(m)));
            head.appendChild(el('span', '', t('monthCount', { count: monthCounts[m] })));
            frag.appendChild(head);
            view.lastMonth = m;
          }
          var card = renderPost(p);
          card.id = 'post-' + view.rendered;   // 按天跳转的定位锚点
          frag.appendChild(card);
          cards.push(card);
          view.rendered++;
        }
        box.appendChild(frag);
        // 逐卡测量：卡片已入文档，引用块 scrollWidth 有效；不重复绑定已渲染卡片
        cards.forEach(function (c) { initQuoteExpand(c); });
        updateSentinel();
      }
      function updateSentinel() {
        var s = get('sentinel');
        var total = view.posts.length;
        if (!total) { s.textContent = ''; return; }
        s.textContent = view.rendered < total
          ? t('loadMore', { shown: view.rendered, total: total })
          : t('loadAll', { total: total });
      }
      function checkSentinel() {
        if (view.rendered >= view.posts.length) return;
        var r = get('sentinel').getBoundingClientRect();
        if (r.top < window.innerHeight + 400) {
          state.limit += PAGE_SIZE;
          appendUntil(state.limit);
          requestAnimationFrame(checkSentinel);
        }
      }
      function initSentinelObserver() {
        var s = get('sentinel');
        if (!('IntersectionObserver' in window)) {
          window.addEventListener('scroll', checkSentinel, { passive: true });
          return;
        }
        new IntersectionObserver(function (entries) {
          if (entries.length && entries[0].isIntersecting) requestAnimationFrame(checkSentinel);
        }, { rootMargin: '400px 0px' }).observe(s);
      }

      function renderList() {
        var box = get('list');
        box.textContent = '';
        if (state.failed) { setStatus(t('loadError', { error: state.failed })); get('sentinel').textContent = ''; return; }
        if (!state.data) {
          setStatus(t('loading'));
          for (var i = 0; i < 3; i++) box.appendChild(el('div', 'tg-skeleton'));
          return;
        }
        setStatus(null);

        var keys = sessionKeys();
        var key = keys.indexOf(state.session) >= 0 ? state.session : keys[0];
        var q = state.query.trim().toLowerCase();
        var posts = allPosts().filter(function (p) {
          return (p.session || 'session3') === key;
        });
        if (q) {
          posts = posts.filter(function (p) {
            var hay = (p.text || '') + '\n' + ((p.reply_context && p.reply_context.text) || '');
            return hay.toLowerCase().indexOf(q) >= 0;
          });
        }
        if (state.sort === 'asc') posts = posts.slice().reverse();

        view.posts = posts;
        view.rendered = 0;
        view.lastMonth = '';

        renderToolbar();
        renderMonthNav();

        if (!posts.length) {
          box.appendChild(el('div', CLS_EMPTY, q ? t('searchEmpty') : t('empty')));
          get('sentinel').textContent = '';
          return;
        }
        appendUntil(state.limit);
        updateMonthHighlight();
        requestAnimationFrame(checkSentinel);
      }

      function setLang(code) {
        state.lang = pick(code, state.cfg);
        save(state.lang);
        /* 列表会整表重建：先停掉跳转残留的锚点滚动，并保住滚动位置与已渲染条数。
           若把 limit 砍回一页，文档高度骤降会让浏览器钳制 scrollY，出现闪动。 */
        stopAnchorScroll();
        var y = window.scrollY;
        renderChrome();
        renderList();
        window.scrollTo(0, y);
      }

      function check(r) {
        if (!r.ok) throw new Error('HTTP ' + r.status);
        return r.json();
      }
      // 允许把语言包 / 数据内联注入做离线预览；生产环境走相对路径 fetch
      function loadJson(rel, inlineKey) {
        var injected = window[inlineKey];
        if (injected) return Promise.resolve(injected);
        return fetch(rel).then(check);
      }

      get('search-input').addEventListener('input', function (e) {
        state.query = e.target.value || '';
        state.limit = PAGE_SIZE;
        renderList();
      });

      get('subscribeBell').innerHTML = iconSvg('bell');
      get('aboutBtn').innerHTML = iconSvg('info');
      get('subscribeClose').innerHTML = iconSvg('x');
      get('aboutClose').innerHTML = iconSvg('x');
      get('backTop').innerHTML = iconSvg('arrowUp');

      /* 联系作者：显示邮箱并点击复制，绕开系统 mailto 处理器 */
      (function () {
        var link = get('contact-link');
        var label = get('contact-label');
        var email = link.getAttribute('data-email');
        link.addEventListener('click', function () {
          copyText(email, function (ok) {
            label.textContent = ok ? t('copyDone') : t('copyFailed');
            clearTimeout(link._copyTimer);
            link._copyTimer = setTimeout(function () { label.textContent = email; }, 1600);
          });
        });
      })();

      get('aboutBtn').addEventListener('click', openAbout);
      get('aboutClose').addEventListener('click', closeAbout);
      get('aboutModal').addEventListener('click', function (e) {
        if (e.target === get('aboutModal')) closeAbout();
      });
      /* 面板内照常滚动；落在遮罩上的滚轮不带动背景页面 */
      get('aboutModal').addEventListener('wheel', function (e) {
        if (!get('aboutPanel').contains(e.target)) e.preventDefault();
      }, { passive: false });

      get('tl-mode-month').addEventListener('click', function () { setTimelineMode('month'); });
      get('tl-mode-day').addEventListener('click', function () { setTimelineMode('day'); });
      get('cal-prev').addEventListener('click', function () { shiftCalMonth(1); });
      get('cal-next').addEventListener('click', function () { shiftCalMonth(-1); });

      /* 月份目录滚轮优先：整块面板都算命中区；容器还能滚时独占滚轮，
         到顶 / 到底后不再拦截，继续滑动自然交回页面 */
      (function () {
        var panel = get('timeline-title').closest('section');
        var box = get('month-nav');
        panel.addEventListener('wheel', function (e) {
          if (e.ctrlKey || state.tlMode !== 'month') return;
          var max = box.scrollHeight - box.clientHeight;
          if (max <= 1) return;
          var dy = e.deltaY;
          if (e.deltaMode === 1) dy *= 16;                     // 行模式
          else if (e.deltaMode === 2) dy *= box.clientHeight;  // 页模式
          dy *= 1.4;                                           // 触控板小位移也明显响应
          if (!dy) return;
          var at = box.scrollTop;
          if ((dy > 0 && at >= max - 1) || (dy < 0 && at <= 1)) return;
          box.scrollTop = Math.max(0, Math.min(max, at + dy));
          e.preventDefault();
        }, { passive: false });
      })();

      get('sortBtn').addEventListener('click', function () {
        state.sort = state.sort === 'desc' ? 'asc' : 'desc';
        state.limit = PAGE_SIZE;
        renderList();
        stopAnchorScroll();
        window.scrollTo({ top: 0, behavior: 'smooth' });
      });

      /* 订阅功能未开放：铃铛 / 卡片 / 表单任一入口都只提示，不记邮箱、不请求服务 */
      function showSubscribeSoon() {
        var card = get('subscribe-card');
        card.classList.remove('tg-dismissed');
        card.classList.add('is-open');
        var fb = get('subscribe-feedback');
        fb.textContent = t('subscribeSoon');
        fb.classList.remove('tg-is-hidden');
      }
      get('subscribe-form').addEventListener('submit', function (e) {
        e.preventDefault();
        showSubscribeSoon();
      });
      get('subscribe-card').addEventListener('click', function (e) {
        if (e.target.closest && e.target.closest('#subscribeClose')) return;
        showSubscribeSoon();
      });
      get('subscribeBell').addEventListener('click', function () {
        showSubscribeSoon();
        try { localStorage.removeItem(SUB_HIDE_KEY); } catch (err) { }
        var card = get('subscribe-card');
        stopAnchorScroll();
        card.scrollIntoView({ behavior: 'smooth', block: 'center' });
        card.classList.add('is-flash');
        setTimeout(function () { card.classList.remove('is-flash'); }, 1600);
      });
      get('subscribeClose').addEventListener('click', function () {
        var card = get('subscribe-card');
        card.classList.add('tg-dismissed');
        card.classList.remove('is-open');
        try { localStorage.setItem(SUB_HIDE_KEY, '1'); } catch (err) { }
      });
      get('backTop').addEventListener('click', function () {
        stopAnchorScroll();
        window.scrollTo({ top: 0, behavior: 'smooth' });
      });
      window.addEventListener('scroll', function () {
        get('backTop').classList.toggle('is-visible', window.scrollY > 600);
        updateMonthHighlight();
      }, { passive: true });
      // 用户主动滚动时立刻让出控制权，避免校正循环跟手势抢滚动
      ['wheel', 'touchstart', 'keydown'].forEach(function (type) {
        window.addEventListener(type, stopAnchorScroll, { passive: true });
      });

      get('lightboxClose').innerHTML = iconSvg('x');
      get('lightboxClose').addEventListener('click', closeLightbox);
      get('lightbox').addEventListener('click', closeLightbox);
      get('lightboxImg').addEventListener('mousedown', function (e) { e.preventDefault(); });
      get('lightboxImg').addEventListener('click', function (e) { e.stopPropagation(); });
      // 不锁 body 滚动，改在放大层拦截滚轮/触摸，背景不会跟着滚
      get('lightbox').addEventListener('wheel', function (e) { e.preventDefault(); }, { passive: false });
      get('lightbox').addEventListener('touchmove', function (e) { e.preventDefault(); }, { passive: false });
      document.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') {
          closeLightbox();
          closeAbout();
          var input = get('search-input');
          if (document.activeElement === input) input.blur();
          return;
        }
        var target = e.target || {};
        var typing = target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable;
        if (typing) return;
        var key = String(e.key || '').toLowerCase();
        if (e.key === '/' || ((e.metaKey || e.ctrlKey) && key === 'k')) {
          e.preventDefault();
          var si = get('search-input');
          si.focus();
          si.select();
        }
      });

      initSentinelObserver();

      // 相对路径：项目仓库 Pages 挂在 /<repo>/ 下也能正常加载
      loadJson('./i18n.json', '__ARCHIVE_I18N__')
        .then(function (cfg) {
          state.cfg = cfg;
          initLangButtons();
          initThemeButton();
          setLang(detect(cfg));
          return loadJson('./data/posts.json', '__ARCHIVE_POSTS__');
        })
        .then(function (data) {
          state.data = data;
          renderChrome();
          renderList();
        })
        .catch(function (e) {
          state.failed = e.message;
          initThemeButton();
          if (state.cfg) { renderChrome(); renderList(); }
          else {
            get('title').textContent = 'Message Archive';
            get('status').textContent = 'Failed to load i18n.json: ' + e.message;
          }
        });
    })();
