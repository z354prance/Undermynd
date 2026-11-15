(function(){
  var all=[],list=document.getElementById('list'),count=document.getElementById('count');
  function el(t,c,x){var e=document.createElement(t);if(c)e.className=c;if(x!=null)e.textContent=x;return e}
  function draw(){
    var q=document.getElementById('q').value.trim().toLowerCase(),k=document.getElementById('kind').value,w=document.getElementById('when').value,now=Date.now();
    var items=all.filter(function(m){
      if(k&&m.kind!==k)return false;
      if(q&&(m.title+' '+(m.detail||'')).toLowerCase().indexOf(q)<0)return false;
      var t=m.date?Date.parse(m.date):0;
      if(w==='aired'&&!(t&&t<=now))return false;
      if(w==='upcoming'&&!(t&&t>now))return false;
      return true});
    count.textContent=items.length+' of '+all.length;
    list.replaceChildren();
    if(!items.length){list.appendChild(el('div','empty','nothing matches'));return}
    items.slice(0,500).forEach(function(m){
      var r=el('div','row');r.appendChild(el('div','name',m.title+(m.detail?'  '+m.detail:'')));
      r.appendChild(el('div','tag',m.kind));r.appendChild(el('div','',m.date?new Date(m.date).toLocaleDateString():''));list.appendChild(r)});
  }
  ['q','kind','when'].forEach(function(id){document.getElementById(id).addEventListener('input',draw)});
  fetch('/api/public/library').then(function(r){return r.json()}).then(function(d){all=d.missing||[];draw()}).catch(function(){count.textContent='unavailable'});
})();
