    /* Liquid glass 折射层（原生版，做法同 commit 31ef5b9）
       1. canvas 逐像素算圆角胶囊的 SDF：只在边缘 bevel 带内产生法线
       2. R/G 编码水平/垂直折射，127.5 为中性，中心保持清晰
       3. feImage 喂给 feDisplacementMap，对 SourceGraphic 做位移
       4. 尺寸变化时重生成，按 WxH 缓存；CSS 变量注入唯一 filter id */
    (function () {
      'use strict';
      var surface = document.getElementById('search-glass');
      if (!surface) return;
      var filter = document.getElementById('lg-search-glass');
      var image = document.getElementById('lg-image');
      var disp = document.getElementById('lg-disp');
      var canvas = document.createElement('canvas');
      var maps = new Map();
      var MAX_MAPS = 32;
      var frame = 0;
      var previousSize = '';

      function displacementMap(width, height) {
        var key = width + 'x' + height;
        if (maps.has(key)) return maps.get(key);
        canvas.width = width;
        canvas.height = height;
        var ctx = canvas.getContext('2d');
        if (!ctx) return null;
        var img = ctx.createImageData(width, height);
        var radius = Math.min(width, height) / 2;
        var bevel = Math.min(radius * 0.6, 14);
        for (var y = 0; y < height; y++) {
          for (var x = 0; x < width; x++) {
            var px = x + 0.5 - width / 2;
            var py = y + 0.5 - height / 2;
            var qx = Math.abs(px) - width / 2 + radius;
            var qy = Math.abs(py) - height / 2 + radius;
            var outsideX = Math.max(qx, 0);
            var outsideY = Math.max(qy, 0);
            var length = Math.hypot(outsideX, outsideY);
            var distance = Math.min(Math.max(qx, qy), 0) + length - radius;
            var depth = Math.max(0, -distance);
            var weight = distance <= 0 ? Math.pow(Math.max(0, 1 - depth / bevel), 2) : 0;
            var nx = 0, ny = 0;
            if (length > 0) {
              nx = (outsideX / length) * Math.sign(px);
              ny = (outsideY / length) * Math.sign(py);
            } else if (qx > qy) {
              nx = Math.sign(px);
            } else {
              ny = Math.sign(py);
            }
            var i = (y * width + x) * 4;
            img.data[i] = Math.round(127.5 - nx * weight * 127.5);
            img.data[i + 1] = Math.round(127.5 - ny * weight * 127.5);
            img.data[i + 2] = 128;
            img.data[i + 3] = 255;
          }
        }
        ctx.putImageData(img, 0, 0);
        var url = canvas.toDataURL();
        if (maps.size >= MAX_MAPS) maps.delete(maps.keys().next().value);
        maps.set(key, url);
        return url;
      }

      function updateMap() {
        frame = 0;
        var bounds = surface.getBoundingClientRect();
        var width = Math.max(1, Math.round(bounds.width));
        var height = Math.max(1, Math.round(bounds.height));
        var size = width + 'x' + height;
        if (size === previousSize) return;
        var url = displacementMap(width, height);
        if (!url) return;
        previousSize = size;
        filter.setAttribute('width', String(width));
        filter.setAttribute('height', String(height));
        image.setAttribute('width', String(width));
        image.setAttribute('height', String(height));
        image.setAttribute('href', url);
        // 旧版 WebKit 的 feImage 只认 xlink:href；同时设置才能让折射真正生效
        image.setAttributeNS('http://www.w3.org/1999/xlink', 'xlink:href', url);
        disp.setAttribute('scale', String(Math.min(height * 0.18, 11) * 2));
        surface.style.setProperty('--liquid-glass-filter', 'url(#lg-search-glass)');
        surface.setAttribute('data-ready', 'true');
      }

      function schedule() {
        if (!frame) frame = requestAnimationFrame(updateMap);
      }

      if (window.ResizeObserver) {
        var observer = new ResizeObserver(schedule);
        observer.observe(surface);
      } else {
        window.addEventListener('resize', schedule);
      }
      updateMap();
    })();
