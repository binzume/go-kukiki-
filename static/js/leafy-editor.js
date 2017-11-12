"use strict";

window.addEventListener('DOMContentLoaded',(function(e){
	console.log("Hello, Leafy.");
	var editor = {
		elem: document.getElementById('editor'),
		getValue: function(){return editor.elem.value;}
	};
	var update = function() {
		var dom = markup(editor.getValue());
		var e = document.getElementById('preview-pane');
		element_clear(e);
		element_append(e, dom);
	};

	if (window.CodeMirror) {
		editor = CodeMirror.fromTextArea(document.getElementById('editor'));
		editor.on("change", update);
		update();
	} else {
		editor.elem.addEventListener('change', update);
		editor.elem.addEventListener('keyup', update);
		update();
	}

}),false);

