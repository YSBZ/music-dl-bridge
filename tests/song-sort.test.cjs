const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../internal/web/templates/static/js/app.js'),'utf8');
const state=source.slice(source.indexOf('let songSortMode ='),source.indexOf('function isAppRoute('));
const sorter=source.slice(source.indexOf('function songSortValue('),source.indexOf('function syncSongSortControls('));
function run(bitrates, mode){
 let cards=bitrates.map((rate,i)=>({id:i,dataset:{sortBitrate:String(rate),sortIndex:String(i)}}));
 const list={matches:()=>true,querySelector:()=>null,querySelectorAll:()=>cards,appendChild:f=>{cards=f.cards}};
 const context=vm.createContext({document:{querySelector:()=>list,createDocumentFragment:()=>({cards:[],appendChild(c){this.cards.push(c)}})},ensureSongSortIndexes:()=>{},parsePositiveInt:(s,f)=>parseInt(s,10)||f});
 vm.runInContext(state+sorter+(mode ? ';songSortMode='+JSON.stringify(mode) : '')+';applySongSort();',context);
 return cards.map(c=>c.id);
}
test('default preserves server relevance and version ranking despite bitrate',()=>{
 assert.deepEqual(run([0,128,320,320,1411]),[0,1,2,3,4]);
});
test('explicit quality sorting ranks higher bitrate first, unknown last, stable ties',()=>{
 assert.deepEqual(run([0,128,320,320,1411],"quality"),[4,2,3,1,0]);
});
test('explicit quality mode sorts refreshed metadata',()=>{
 assert.deepEqual(run([192,0,900],"quality"),[2,0,1]);
});
