export namespace main {
	
	export class ProcessOptions {
	    videoPath: string;
	    startTime: number;
	    endTime: number;
	    fps: number;
	    borderWidth: number;
	    borderColor: string;
	    colors: number;
	    dither: string;
	    bayerScale: number;
	    scale: number;
	    monochrome: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProcessOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.videoPath = source["videoPath"];
	        this.startTime = source["startTime"];
	        this.endTime = source["endTime"];
	        this.fps = source["fps"];
	        this.borderWidth = source["borderWidth"];
	        this.borderColor = source["borderColor"];
	        this.colors = source["colors"];
	        this.dither = source["dither"];
	        this.bayerScale = source["bayerScale"];
	        this.scale = source["scale"];
	        this.monochrome = source["monochrome"];
	    }
	}
	export class ProcessResult {
	    panels: string[];
	    files: string[];
	    sizes: number[];
	
	    static createFrom(source: any = {}) {
	        return new ProcessResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.panels = source["panels"];
	        this.files = source["files"];
	        this.sizes = source["sizes"];
	    }
	}
	export class Settings {
	    fps: number;
	    borderWidth: number;
	    borderColor: string;
	    colors: number;
	    dither: string;
	    bayerScale: number;
	    scale: number;
	    monochrome: boolean;
	    outputDir: string;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fps = source["fps"];
	        this.borderWidth = source["borderWidth"];
	        this.borderColor = source["borderColor"];
	        this.colors = source["colors"];
	        this.dither = source["dither"];
	        this.bayerScale = source["bayerScale"];
	        this.scale = source["scale"];
	        this.monochrome = source["monochrome"];
	        this.outputDir = source["outputDir"];
	    }
	}
	export class VideoInfo {
	    width: number;
	    height: number;
	    duration: number;
	    fileSize: number;
	    fps: number;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new VideoInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.width = source["width"];
	        this.height = source["height"];
	        this.duration = source["duration"];
	        this.fileSize = source["fileSize"];
	        this.fps = source["fps"];
	        this.path = source["path"];
	    }
	}

}

