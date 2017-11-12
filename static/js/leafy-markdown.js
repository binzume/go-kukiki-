"use strict";

// utils
function element_append(e, v) {
	if (v instanceof Array) {
		for (var i = 0; i < v.length; i++) {
			element_append(e, v[i]);
		}
	} else {
		e.appendChild((typeof v == 'string')?document.createTextNode(v):v);
	}
}

function element_clear(e) {
	while (e.firstChild) {
	    e.removeChild(e.firstChild);
	}
}

function element(tag, values, attr) {
	var e = document.createElement(tag);
	if (values) {
		element_append(e, values);
	}
	if (typeof(attr) == 'function') {
		attr(e);
	} else if (typeof(attr) == 'object') {
		for (var key in attr) {
			e[key] = attr[key];
		}
	}
	return e;
}

//  simple markup

function markup_end(r, str, chain) {
	r.push(str);
}

function markup_decoration(r, str, chain) {
	var re = new RegExp(/(\\?)(\*\*|~~|__|`+)(?=[^\s])(.*?)\2/g);
	var p = 0, m = null;
	while(m = re.exec(str)) {
		if (m[1] != "") continue;
		var skip = m.index - p;
		if (skip > 0) {
			chain[0](r, str.substr(p, skip), chain.slice(1));
		}
		if (m[2]=="**") {
			r.push(element('strong', m[3]));
		} else if (m[2]=="~~") {
			r.push(element('strike', m[3]));
		} else if (m[2]=="__") {
			r.push(element('i', m[3]));
		} else { // ``
			r.push(element('code', m[3], {className: 'inline-code'}));
		}
		p = re.lastIndex;
	}
	if (p < str.length) {
		chain[0](r, str.substr(p), chain.slice(1));
	}
}

function markup_link(r, str, chain) {
	var re = new RegExp(/(!?)\[(.*?)\]\((.*?)\)/g);
	var p = 0, m = null;
	while(m = re.exec(str)) {
		var skip = m.index - p;
		if (skip > 0) {
			chain[0](r, str.substr(p, skip), chain.slice(1));
		}
		if (m[1]=="") {
			r.push(element('a', m[2], {"href": m[3], 'target':'_blank'}));
		} else {
			r.push(element('img', "", {"src": m[3], 'title':m[2], 'target':'_blank'}));
		}
		p = re.lastIndex;
	}
	if (p < str.length) {
		chain[0](r, str.substr(p), chain.slice(1));
	}
}

function markup_keywords(r, str, chain) {
	var m = [];
	for (var i=0; i<keywords.length; i++) {
		var p = str.indexOf(keywords[i]);
		if (p>=0) m.push([keywords[i], p]);
	}
	var ofs = 0;
	while (m.length) {
		m.sort(function(a,b){return a[1]-b[1]});
		while(m[0] && m[0][1] < ofs) m.shift();
		if (m.length == 0) break;
		var p = m[0][1]-ofs;
		if (p > 0) {
			chain[0](r, str.substr(0, p), chain.slice(1));
		}
		r.push(element('span', str.substr(p, m[0][0].length)+"!", {'className':"keyword"}));
		str = str.substr(p + m[0][0].length);
		ofs += p + m[0][0].length;
		m[0][1] = str.indexOf(m[0][0]) + ofs;
	}
	if (str.length > 0) {
		chain[0](r, str, chain.slice(1));
	}
}

function markup_autolink(r, str, chain) {
	var re = new RegExp(/https?:[^\s\"\']+/g);
	var p = 0, m = null;
	while(m = re.exec(str)) {
		var skip = m.index - p;
		if (skip > 0) {
			chain[0](r, str.substr(p, skip), chain.slice(1));
		}
		r.push(element('a', m[0], {"href": m[0], "target":"_blank"}));
		p = re.lastIndex;
	}
	if (p < str.length) {
		chain[0](r, str.substr(p), chain.slice(1));
	}
}

function markup_p(r, str, chain) {
	var ss = str.split(/\n{2}/);
	for (var i = 0; i < ss.length; i++) {
		var rr = markup_chain(ss[i], chain);
		r.push(element('p', rr));
	}
}


function markup_h(r, str, chain) {
	var re = new RegExp(/(^|\n)(#{1,4})\s*(.*)\n*/gm);
	var p = 0, m = null;
	while(m = re.exec(str)) {
		var skip = m.index - p;
		if (skip > 0) {
			chain[0](r, str.substr(p, skip), chain.slice(1));
		}
		r.push(element('h'+m[2].length, m[3]));
		p = re.lastIndex;
	}
	if (p < str.length) {
		chain[0](r, str.substr(p), chain.slice(1));
	}
}

function markup_codeblock(r, str, chain) {
	var re = new RegExp(/^(~~~|```)(\s\w+)?$((.|\n)+?)^\1$/gm);
	var p = 0, m = null;
	while(m = re.exec(str)) {
		var skip = m.index - p;
		if (skip > 0) {
			chain[0](r, str.substr(p, skip), chain.slice(1));
		}
		r.push(element('pre', element('code', m[3].trim())));
		p = re.lastIndex;
	}
	if (p < str.length) {
		chain[0](r, str.substr(p), chain.slice(1));
	}
}

function markup_checkbox(r, str, chain) {
	var re = new RegExp(/^\s*\[(x| )\]\s+(.*)/gm);
	var p = 0, m = null;
	while(m = re.exec(str)) {
		var skip = m.index - p;
		if (skip > 0) {
			chain[0](r, str.substr(p, skip), chain.slice(1));
		}
		var attr = {"type":"checkbox", "disabled":"disabled"};
		if (m[1] == 'x') {
			attr["checked"] = "checked";
		}
		r.push(element('label', [element('input','', attr),m[2].trim()]));
		p = re.lastIndex;
	}
	if (p < str.length) {
		chain[0](r, str.substr(p), chain.slice(1));
	}
}

function markup_li(r, str, chain) {
	var re = new RegExp(/(^|\n)([ \t]*)([\*\-]|\d+\.)\s+((.|\n\2[ \t])*)/gm);
	var p = 0, m = null;
	var rr = [];
	var listtag = 'ul';
	while(m = re.exec(str)) {
		var skip = m.index - p;
		if (skip > 0) {
			if (rr.length > 0) {
				r.push(element(listtag, rr));
				rr = [];
			}
			chain[0](r, str.substr(p, skip).trim(), chain.slice(1));
		}
		var rrr = [];
		markup_li(rrr, m[4].trim(), chain);
		rr.push(element('li', rrr));
		p = re.lastIndex;
		listtag = m[3].match(/[\-\*]/) ? 'ul' : 'ol';
	}
	if (rr.length > 0) {
		r.push(element(listtag, rr));
		rr = [];
	}
	if (p < str.length) {
		chain[0](r, str.substr(p).trim(), chain.slice(1));
	}
}

function markup_table(r, str, chain) {
	var re = new RegExp(/^\|(.*)\|$\n?/gm);
	var p = 0, m = null;
	var rr = [];
	while(m = re.exec(str)) {
		var skip = m.index - p;
		if (skip > 0) {
			if (rr.length > 0) {
				r.push(element('table', rr));
				rr = [];
			}
			chain[0](r, str.substr(p, skip).trim(), chain.slice(1));
		}
		var cols = m[1].split('|');
		var td = [];
		for (var i=0; i< cols.length; i++) {
			var rrr = [];
			chain[2](rrr,cols[i].trim(), chain.slice(3)); // skip 'p', 'li'
			td.push(element('td', rrr, function(e){if(cols[i].startsWith('  '))e.style.textAlign='right'}));
		}
		rr.push(element('tr', td));
		p = re.lastIndex;
	}
	if (rr.length > 0) {
		r.push(element('table', rr));
		rr = [];
	}
	if (p < str.length) {
		chain[0](r, str.substr(p).trim(), chain.slice(1));
	}
}

function markup_chain(str, chain) {
	var r = [];
	chain[0](r, str, chain.slice(1));
	return r;
}

function markup_inline(str) {
	var chain=[markup_link, markup_autolink, markup_decoration, markup_end];
	return markup_chain(str, chain);
}

function markup(str) {
	var chain=[markup_codeblock, markup_h, markup_table, markup_li, markup_p, markup_checkbox, markup_link, markup_autolink, markup_decoration, markup_end];
	return markup_chain(str, chain);
}

