package utils

import (
	"github.com/gorilla/mux"
	"log"
	"reflect"
	"runtime"
	"strings"
)

func RoutesSummary(r *mux.Router, logger *log.Logger) {
	err := r.Walk(func(route *mux.Route, router *mux.Router, ancestors []*mux.Route) error {
		pathTemplate, err := route.GetPathTemplate()
		if err == nil {
			logger.Println("ROUTE:", pathTemplate)
		}
		pathRegexp, err := route.GetPathRegexp()
		if err == nil {
			logger.Println("Path regexp:", pathRegexp)
		}
		queriesTemplates, err := route.GetQueriesTemplates()
		if err == nil {
			logger.Println("Queries templates:", strings.Join(queriesTemplates, ","))
		}
		// queriesRegexps, err := route.GetQueriesRegexp()
		// if err == nil {
		// 	logger.Println("Queries regexps:", strings.Join(queriesRegexps, ","))
		// }
		methods, err := route.GetMethods()
		if err == nil {
			logger.Println("Methods:", strings.Join(methods, ","))
		}
		if v := reflect.ValueOf(route.GetHandler()); v.Kind() == reflect.Func {
			logger.Println("HandlerFn: ", runtime.FuncForPC(v.Pointer()).Name())
		}
		logger.Println()
		return nil
	})

	if err != nil {
		logger.Println(err)
	}
}
