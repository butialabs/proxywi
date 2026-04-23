const sass = require('sass');
const path = require('path');
const fs = require('fs');

const DIST = 'internal/server/gui/dist';

module.exports = function (grunt) {
  grunt.initConfig({
    clean: {
      dist: [DIST]
    },

    copy: {
      fonts: {
        files: [{
          expand: true,
          cwd: 'node_modules/bootstrap-icons/font/fonts/',
          src: ['*.woff', '*.woff2'],
          dest: `${DIST}/fonts/`
        }]
      },
      img: {
        files: [{
          expand: true,
          cwd: 'source/img/',
          src: ['**/*'],
          dest: `${DIST}/img/`
        }]
      }
    },

    sass: {
      dist: {
        src: 'source/scss/main.scss',
        dest: `${DIST}/css/app.css`
      }
    },

    cssmin: {
      options: {
        level: { 1: { specialComments: 0 } }
      },
      dist: {
        files: {
          [`${DIST}/css/app.min.css`]: `${DIST}/css/app.css`
        }
      }
    },

    concat: {
      options: {
        separator: ';\n'
      },
      dist: {
        src: [
          'node_modules/bootstrap/dist/js/bootstrap.bundle.min.js',
          'node_modules/chart.js/dist/chart.umd.js',
          'source/js/*.js'
        ],
        dest: `${DIST}/js/app.js`
      }
    },

    terser: {
      options: {
        compress: true,
        mangle: true,
        format: { comments: false }
      },
      dist: {
        files: {
          [`${DIST}/js/app.min.js`]: [`${DIST}/js/app.js`]
        }
      }
    },

    watch: {
      scss: {
        files: ['source/scss/**/*.scss'],
        tasks: ['sass', 'cssmin']
      },
      js: {
        files: ['source/js/**/*.js'],
        tasks: ['concat', 'terser']
      },
      img: {
        files: ['source/img/**/*'],
        tasks: ['copy:img']
      }
    }
  });

  grunt.loadNpmTasks('grunt-contrib-clean');
  grunt.loadNpmTasks('grunt-contrib-copy');
  grunt.loadNpmTasks('grunt-contrib-cssmin');
  grunt.loadNpmTasks('grunt-contrib-concat');
  grunt.loadNpmTasks('grunt-terser');
  grunt.loadNpmTasks('grunt-contrib-watch');

  grunt.registerMultiTask('sass', 'Compile Sass via Dart Sass modern API', function () {
    const { src, dest } = this.data;
    const result = sass.compile(src, {
      loadPaths: ['node_modules'],
      style: 'expanded',
      quietDeps: true
    });
    fs.mkdirSync(path.dirname(dest), { recursive: true });
    fs.writeFileSync(dest, result.css);
    grunt.log.writeln(`File ${dest} created.`);
  });

  grunt.registerTask('build', ['clean', 'copy', 'sass', 'cssmin', 'concat', 'terser']);
  grunt.registerTask('default', ['build']);
};
