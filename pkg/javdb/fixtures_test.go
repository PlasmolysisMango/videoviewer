package javdb

// HTML fixtures reproduce the page shapes verified against a live mirror:
// a listing page (search / ranking), a /v/{id} detail page and an actor grid.
// Keeping them as literals makes the parsers testable offline.

const fixtureListingHTML = `<!DOCTYPE html>
<html lang="zh-Hant">
<head><title>有碼排行榜 - JavDB</title></head>
<body>
<div class="movie-list h cols-4 vcols-8">
  <div class="item">
    <a class="box" href="/v/yxY7kW" title="SSIS-001 標題一">
      <div class="cover">
        <figure class="image"><img src="https://www.javdb.com/rhe951l4q/images/yxY7kW_cover.jpg"></figure>
        <span class="ranking">1</span>
      </div>
      <div class="video-title"><strong>SSIS-001</strong> 標題一</div>
      <div class="score"><span class="value"> <span class="icon"></span> <span class="value">4.45分, 由386人評價</span> </span></div>
      <div class="meta">2026-09-15</div>
      <div class="tags has-addons">
        <span class="tag is-success">含中字磁鏈</span>
        <span class="tag is-white">巨乳</span>
      </div>
    </a>
  </div>
  <div class="item">
    <a class="box" href="/v/0eEZqk">
      <div class="cover"><figure class="image"><img data-src="/images/0eEZqk_cover.jpg"></figure></div>
      <div class="video-title"><strong>MIDE-842</strong> 標題二</div>
      <div class="meta">2026-09-14 / 片商A</div>
      <div class="tags has-addons"><span class="tag is-info">可播放</span></div>
    </a>
  </div>
  <div class="item">
    <a class="box" href="/v/Fc2Match">
      <div class="video-title">FC2-PPV-1234567 無碼作品</div>
      <div class="meta">2026-09-13</div>
    </a>
  </div>
</div>
<nav class="pagination is-centered" role="navigation">
  <a class="pagination-previous" href="?page=1">previous</a>
  <a class="pagination-link is-current" aria-current="page" href="?page=2">2</a>
  <a class="pagination-link" href="?page=3">3</a>
  <a class="pagination-link" href="?page=7">7</a>
  <a class="pagination-next" rel="next" href="?page=3">next</a>
</nav>
</body></html>`

const fixtureDetailHTML = `<!DOCTYPE html>
<html lang="zh-Hant">
<head><title>SSIS-001 標題一 - JavDB</title></head>
<body>
<h2 class="title"><strong class="current-title">SSIS-001 標題一</strong></h2>
<div class="video-meta-panel">
  <div class="column-video-cover">
    <img class="video-cover" src="https://www.javdb.com/rhe951l4q/images/yxY7kW_poster.jpg">
  </div>
  <div class="panel-block"><strong>番號:</strong> <span class="value"><a href="/video_codes/SSIS">SSIS</a>-001</span></div>
  <div class="panel-block"><strong>日期:</strong> <span class="value">2026-09-15</span></div>
  <div class="panel-block"><strong>時長:</strong> <span class="value">120分钟</span></div>
  <div class="panel-block"><strong>片商:</strong> <span class="value"><a href="/makers/zKW">片商A</a></span></div>
  <div class="panel-block"><strong>發行商:</strong> <span class="value"><a href="/publishers/aB3">發行B</a></span></div>
  <div class="panel-block"><strong>系列:</strong> <span class="value"><a href="/series/xY9">系列C</a></span></div>
  <div class="panel-block"><strong>導演:</strong> <span class="value"><a href="/directors/d1">導演D</a></span></div>
  <div class="panel-block"><strong>評分:</strong> <span class="value">4.45分, 由386人評價</span></div>
  <div class="panel-block"><strong>類別:</strong> <span class="value">
    <a href="/tags?c4=15">巨乳</a>, <a href="/tags?c4=88">高中職生</a></span></div>
  <div class="panel-block"><strong>演員:</strong> <span class="value">
    <a href="/actors/21Jp">楓花戀</a><strong class="symbol female">♀</strong>,
    <a href="/actors/kzx6">第二人</a><strong class="symbol female">♀</strong></span></div>
</div>
<div class="columns">
  <div class="column">
    <figure class="image"><video id="preview-video" src="blob:https://x/y"></video></figure>
  </div>
</div>
<div class="tile-images">
  <a class="tile-item" href="https://x/rhe951l4q/preview/1_large.jpg"><img src="https://x/rhe951l4q/preview/1_thumb.jpg"></a>
  <a class="tile-item" href="https://x/rhe951l4q/preview/2_large.jpg"><img src="https://x/rhe951l4q/preview/2_thumb.jpg"></a>
</div>
<a class="review-tab">25</a>
<span class="is-size-7">1234 人想看</span>
<span class="is-size-7">567 人看過</span>
<div id="magnets-content">
  <div class="item"><div class="columns is-desktop">
    <div class="column">
      <div class="magnet-name">
        <a href="magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&amp;dn=SSIS-001">
          <span class="name">SSIS-001 中文字幕 [4K]</span>
          <span class="meta">2.50GB, 3 個文件</span>
        </a>
        <span class="tags"><span class="tag is-small is-link">含中字</span></span>
      </div>
      <time><span class="time" title="2026-09-16 12:00">1 天前</span></time>
    </div>
  </div></div>
  <div class="item sda-content"><div class="magnet-name"><a href="#"><span class="name">廣告</span></a></div></div>
  <div class="item"><div class="columns is-desktop">
    <div class="column">
      <div class="magnet-name">
        <a href="magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678">
          <span class="name">SSIS-001 無碼破解 [720p]</span>
          <span class="meta">1.24GB, 1 個文件</span>
        </a>
        <span class="tags"><span class="tag is-small">高清</span></span>
      </div>
      <time><span class="time">2026-09-10</span></time>
    </div>
  </div></div>
</div>
</body></html>`

const fixtureActorsHTML = `<!DOCTYPE html>
<html><head><title>有碼女优 - JavDB</title></head>
<body>
<div id="actors" class="actors">
  <div class="box actor-box"><a href="/actors/kzx6" title="小明, 別名A,別名B">
    <figure class="image"><img class="avatar" src="https://x/rhe951l4q/avatars/kzx6.jpg"></figure>
    <strong>小明</strong><span class="video-count">128</span>
  </a></div>
  <div class="box actor-box"><a href="/actors/21Jp" title="楓花戀">
    <figure class="image"><img class="avatar" data-src="/avatars/21Jp.jpg"></figure>
    <strong>楓花戀</strong>
  </a></div>
</div>
<nav class="pagination"><a class="pagination-link is-current" href="?page=1">1</a><a class="pagination-next" href="?page=2">next</a></nav>
</body></html>`

const fixtureLoginWallHTML = `<script>window.location.href='/login';</script>`

// fixtureLoginWallTitleHTML is the page the mirrors really return to an
// anonymous client: the <title> is padded with spaces, so a raw substring
// match on "<title>登入" misses it and the response used to be parsed as an
// ordinary (empty) listing.
const fixtureLoginWallTitleHTML = `<!DOCTYPE html>
<html lang="zh-Hant">
<head><title> 登入 | JavDB 成人影片數據庫 </title></head>
<body><div class="hero-body"><div class="container"><h1 class="title">登入</h1>
<form action="/users/sign_in" method="post">
<input type="email" name="user[email]"><input type="password" name="user[password]">
</form></div></div></body></html>`

const fixtureGenreHTML = `<!DOCTYPE html>
<html><head><title>類別 - JavDB</title></head><body>
<div class="columns">
  <a href="/tags?c4=15">巨乳</a>
  <a href="/tags?c4=151">制服扮演</a>
  <a href="/tags/abc">亂碼</a>
</div>
</body></html>`
